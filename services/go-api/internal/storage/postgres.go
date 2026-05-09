package storage

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"demo-number-plates/services/go-api/internal/config"
	"demo-number-plates/services/go-api/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
	cfg  config.Config
}

func NewPostgresStore(ctx context.Context, cfg config.Config) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, cfg.PostgresURL)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	return &PostgresStore{pool: pool, cfg: cfg}, nil
}

func (s *PostgresStore) Close() {
	s.pool.Close()
}

func (s *PostgresStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *PostgresStore) Migrate(ctx context.Context) error {
	query := `
	CREATE TABLE IF NOT EXISTS plates (
		id BIGSERIAL PRIMARY KEY,
		plate_number VARCHAR(32) UNIQUE NOT NULL,
		display_plate VARCHAR(32) NOT NULL,
		plate_type VARCHAR(16) NOT NULL CHECK (plate_type IN ('standard', 'vanity')),
		serial_number INTEGER,
		vanity_text VARCHAR(7),
		is_reserved BOOLEAN DEFAULT FALSE,
		created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_plate_number ON plates (plate_number);
	CREATE INDEX IF NOT EXISTS idx_plate_standard ON plates (plate_type, serial_number);
	CREATE INDEX IF NOT EXISTS idx_plate_vanity ON plates (plate_type, vanity_text);
	`
	_, err := s.pool.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	return nil
}

func (s *PostgresStore) PlateCount(ctx context.Context) (int64, error) {
	var count int64
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM plates`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count plates: %w", err)
	}
	return count, nil
}

func (s *PostgresStore) ExistsCanonical(ctx context.Context, canonicalKey string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plates WHERE plate_number = $1)`, canonicalKey).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("lookup plate: %w", err)
	}
	return exists, nil
}

func (s *PostgresStore) ImportIfEmpty(ctx context.Context) (bool, error) {
	count, err := s.PlateCount(ctx)
	if err != nil {
		return false, err
	}
	if count > 0 || !s.cfg.ImportEnabled {
		return false, nil
	}
	if err := s.importFromDataDir(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *PostgresStore) StreamRecords(ctx context.Context, fn func(model.PlateRecord) error) error {
	rows, err := s.pool.Query(ctx, `
		SELECT plate_number, display_plate, plate_type, COALESCE(serial_number, 0), COALESCE(vanity_text, '')
		FROM plates
		ORDER BY id ASC
	`)
	if err != nil {
		return fmt.Errorf("query plates for sync: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var record model.PlateRecord
		var plateType string
		if err := rows.Scan(&record.CanonicalKey, &record.DisplayPlate, &plateType, &record.SerialNumber, &record.VanityText); err != nil {
			return fmt.Errorf("scan plate record: %w", err)
		}
		record.PlateType = model.PlateType(plateType)
		if record.PlateType == model.PlateTypeStandard {
			record.TownCode = standardTownFromCanonical(record.CanonicalKey)
		} else {
			record.VanityText = strings.ToUpper(record.VanityText)
		}
		if err := fn(record); err != nil {
			return err
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate plate records: %w", err)
	}
	return nil
}

func (s *PostgresStore) importFromDataDir(ctx context.Context) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin import transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `TRUNCATE TABLE plates RESTART IDENTITY`); err != nil {
		return fmt.Errorf("truncate plates before import: %w", err)
	}

	standardPath := filepath.Join(s.cfg.DataDir, "standard_plates.txt")
	standardCount, err := s.importStandardFile(ctx, tx, standardPath)
	if err != nil {
		return err
	}
	vanityPath := filepath.Join(s.cfg.DataDir, "vanity_plates.txt")
	vanityCount, err := s.importVanityFile(ctx, tx, vanityPath)
	if err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit import transaction: %w", err)
	}

	log.Printf("import complete: standard=%d vanity=%d total=%d", standardCount, vanityCount, standardCount+vanityCount)
	return nil
}

type copyFromExecutor interface {
	CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error)
}

func (s *PostgresStore) importStandardFile(ctx context.Context, executor copyFromExecutor, path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open standard data file: %w", err)
	}
	defer file.Close()

	seen := make(map[string]struct{}, s.cfg.ImportBatchSize*2)
	buffer := make([][]any, 0, s.cfg.ImportBatchSize)
	imported := 0
	flush := func() error {
		if len(buffer) == 0 {
			return nil
		}
		rowsCopied, err := executor.CopyFrom(
			ctx,
			pgx.Identifier{"plates"},
			[]string{"plate_number", "display_plate", "plate_type", "serial_number", "vanity_text", "is_reserved"},
			pgx.CopyFromRows(buffer),
		)
		if err != nil {
			return fmt.Errorf("copy standard data rows: %w", err)
		}
		imported += int(rowsCopied)
		if imported%s.cfg.ImportBatchSize == 0 {
			log.Printf("standard import progress: %d rows", imported)
		}
		buffer = buffer[:0]
		return nil
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		record, ok := parseStandardDataLine(scanner.Text())
		if !ok {
			continue
		}
		if _, exists := seen[record.CanonicalKey]; exists {
			continue
		}
		seen[record.CanonicalKey] = struct{}{}
		buffer = append(buffer, []any{record.CanonicalKey, record.DisplayPlate, string(record.PlateType), record.SerialNumber, nil, false})
		if len(buffer) >= s.cfg.ImportBatchSize {
			if err := flush(); err != nil {
				return 0, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan standard data file: %w", err)
	}
	if err := flush(); err != nil {
		return 0, err
	}
	return imported, nil
}

func (s *PostgresStore) importVanityFile(ctx context.Context, executor copyFromExecutor, path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open vanity data file: %w", err)
	}
	defer file.Close()

	seen := make(map[string]struct{}, s.cfg.ImportBatchSize*2)
	buffer := make([][]any, 0, s.cfg.ImportBatchSize)
	imported := 0
	flush := func() error {
		if len(buffer) == 0 {
			return nil
		}
		rowsCopied, err := executor.CopyFrom(
			ctx,
			pgx.Identifier{"plates"},
			[]string{"plate_number", "display_plate", "plate_type", "serial_number", "vanity_text", "is_reserved"},
			pgx.CopyFromRows(buffer),
		)
		if err != nil {
			return fmt.Errorf("copy vanity data rows: %w", err)
		}
		imported += int(rowsCopied)
		if imported%s.cfg.ImportBatchSize == 0 {
			log.Printf("vanity import progress: %d rows", imported)
		}
		buffer = buffer[:0]
		return nil
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		record, ok := parseVanityDataLine(scanner.Text())
		if !ok {
			continue
		}
		if _, exists := seen[record.CanonicalKey]; exists {
			continue
		}
		seen[record.CanonicalKey] = struct{}{}
		buffer = append(buffer, []any{record.CanonicalKey, record.DisplayPlate, string(record.PlateType), nil, record.VanityText, false})
		if len(buffer) >= s.cfg.ImportBatchSize {
			if err := flush(); err != nil {
				return 0, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan vanity data file: %w", err)
	}
	if err := flush(); err != nil {
		return 0, err
	}
	return imported, nil
}

func parseStandardDataLine(raw string) (model.PlateRecord, bool) {
	cleaned := uppercaseAlphaNumeric(raw)
	cleaned = strings.TrimPrefix(cleaned, "N")
	if cleaned == "" {
		return model.PlateRecord{}, false
	}
	letterStart := len(cleaned)
	for letterStart > 0 {
		ch := cleaned[letterStart-1]
		if ch < 'A' || ch > 'Z' {
			break
		}
		letterStart--
	}
	if letterStart == 0 || letterStart == len(cleaned) {
		return model.PlateRecord{}, false
	}
	townCode := cleaned[letterStart:]
	if len(townCode) > 3 {
		return model.PlateRecord{}, false
	}
	serialPart := cleaned[:letterStart]
	serialNumber, err := strconv.Atoi(serialPart)
	if err != nil || serialNumber < 1 || serialNumber > 999999 {
		return model.PlateRecord{}, false
	}
	return model.PlateRecord{
		CanonicalKey: formatStandardCanonical(townCode, serialNumber),
		DisplayPlate: formatStandardDisplay(townCode, serialNumber),
		PlateType:    model.PlateTypeStandard,
		TownCode:     townCode,
		SerialNumber: serialNumber,
	}, true
}

func parseVanityDataLine(raw string) (model.PlateRecord, bool) {
	cleaned := uppercaseAlphaNumeric(raw)
	if cleaned == "" {
		return model.PlateRecord{}, false
	}
	if len(cleaned) > 7 {
		cleaned = cleaned[:7]
	}
	return model.PlateRecord{
		CanonicalKey: formatVanityCanonical(cleaned),
		DisplayPlate: formatVanityDisplay(cleaned),
		PlateType:    model.PlateTypeVanity,
		VanityText:   cleaned,
	}, true
}

func uppercaseAlphaNumeric(raw string) string {
	raw = strings.ToUpper(strings.TrimSpace(raw))
	b := strings.Builder{}
	b.Grow(len(raw))
	for _, r := range raw {
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
			continue
		}
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func formatStandardCanonical(town string, serial int) string {
	return fmt.Sprintf("STD:%s:%d", strings.ToUpper(town), serial)
}

func formatStandardDisplay(town string, serial int) string {
	raw := fmt.Sprintf("%d", serial)
	if len(raw) == 6 {
		raw = raw[:3] + "-" + raw[3:]
	}
	return fmt.Sprintf("N %s %s", raw, strings.ToUpper(town))
}

func formatVanityCanonical(vanity string) string {
	return fmt.Sprintf("VTY:%s", strings.ToUpper(vanity))
}

func formatVanityDisplay(vanity string) string {
	return fmt.Sprintf("%s NA", strings.ToUpper(vanity))
}

func standardTownFromCanonical(canonical string) string {
	parts := strings.SplitN(canonical, ":", 3)
	if len(parts) != 3 {
		return ""
	}
	return parts[1]
}
