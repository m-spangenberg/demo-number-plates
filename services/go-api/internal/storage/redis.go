package storage

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"demo-number-plates/services/go-api/internal/config"
	"demo-number-plates/services/go-api/internal/model"

	"github.com/redis/go-redis/v9"
)

const namespace = "plates:v1"

type RedisStore struct {
	client      *redis.Client
	cfg         config.Config
	bloomBits   uint64
	bloomHashes uint64
}

func NewRedisStore(ctx context.Context, cfg config.Config) (*RedisStore, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}
	bits, hashes := bloomShape(cfg.BloomExpectedItems, cfg.BloomFalsePositive)
	return &RedisStore{client: client, cfg: cfg, bloomBits: bits, bloomHashes: hashes}, nil
}

func (s *RedisStore) Close() error {
	return s.client.Close()
}

func (s *RedisStore) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}

func (s *RedisStore) AllowRequest(ctx context.Context, identity string) (bool, int, error) {
	windowSeconds := int64(s.cfg.RateLimitWindow / time.Second)
	if windowSeconds <= 0 {
		windowSeconds = 60
	}
	windowSlot := time.Now().UTC().Unix() / windowSeconds
	key := fmt.Sprintf("%s:ratelimit:%s:%d", namespace, sanitizeRedisKey(identity), windowSlot)
	pipe := s.client.TxPipeline()
	countCmd := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, s.cfg.RateLimitWindow+5*time.Second)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return false, 0, fmt.Errorf("increment rate limit: %w", err)
	}
	count := int(countCmd.Val())
	remaining := s.cfg.RateLimitRequests - count
	if remaining < 0 {
		remaining = 0
	}
	return count <= s.cfg.RateLimitRequests, remaining, nil
}

func (s *RedisStore) BuildIndexes(ctx context.Context, source *PostgresStore) error {
	if !s.cfg.RedisSyncEnabled {
		return nil
	}
	if err := s.flushNamespace(ctx); err != nil {
		return err
	}
	if err := s.client.HSet(ctx, s.bloomMetaKey(), map[string]any{
		"bits":   s.bloomBits,
		"hashes": s.bloomHashes,
	}).Err(); err != nil {
		return fmt.Errorf("store bloom metadata: %w", err)
	}

	pipe := s.client.Pipeline()
	queued := 0
	flush := func() error {
		if queued == 0 {
			return nil
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("flush redis indexes: %w", err)
		}
		queued = 0
		return nil
	}

	err := source.StreamRecords(ctx, func(record model.PlateRecord) error {
		if s.cfg.RedisSyncBloom {
			for _, position := range s.bloomPositions(record.CanonicalKey) {
				pipe.SetBit(ctx, s.bloomKey(), int64(position), 1)
				queued++
			}
		}
		if record.PlateType == model.PlateTypeStandard && s.cfg.RedisSyncBitmap && record.TownCode != "" {
			pipe.SetBit(ctx, s.bitmapKey(record.TownCode), int64(record.SerialNumber), 1)
			queued++
		}
		if record.PlateType == model.PlateTypeVanity && s.cfg.RedisSyncTrie {
			prefix := ""
			for _, r := range strings.ToUpper(record.VanityText) {
				child := string(r)
				pipe.HSet(ctx, s.trieNodeKey(prefix), "c:"+child, 1)
				queued++
				prefix += child
			}
			pipe.HSet(ctx, s.trieNodeKey(prefix), "_t", 1)
			queued++
		}
		if queued >= 5000 {
			return flush()
		}
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

func (s *RedisStore) BloomMightContain(ctx context.Context, canonical string) (bool, error) {
	if !s.cfg.RedisSyncBloom {
		return true, nil
	}
	positions := s.bloomPositions(canonical)
	pipe := s.client.Pipeline()
	commands := make([]*redis.IntCmd, 0, len(positions))
	for _, position := range positions {
		commands = append(commands, pipe.GetBit(ctx, s.bloomKey(), int64(position)))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("check bloom filter: %w", err)
	}
	for _, command := range commands {
		if command.Val() == 0 {
			return false, nil
		}
	}
	return true, nil
}

func (s *RedisStore) StandardBitmapContains(ctx context.Context, town string, serial int) (bool, error) {
	if !s.cfg.RedisSyncBitmap {
		return false, nil
	}
	value, err := s.client.GetBit(ctx, s.bitmapKey(town), int64(serial)).Result()
	if err != nil {
		return false, fmt.Errorf("check standard bitmap: %w", err)
	}
	return value == 1, nil
}

func (s *RedisStore) VanityTrieContains(ctx context.Context, vanity string) (bool, error) {
	if !s.cfg.RedisSyncTrie {
		return false, nil
	}
	exists, err := s.client.HExists(ctx, s.trieNodeKey(strings.ToUpper(vanity)), "_t").Result()
	if err != nil {
		return false, fmt.Errorf("check vanity trie: %w", err)
	}
	return exists, nil
}

func (s *RedisStore) SuggestVanity(ctx context.Context, prefix string, limit int) ([]string, error) {
	if !s.cfg.RedisSyncTrie {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}
	prefix = strings.ToUpper(prefix)
	nodeKey := s.trieNodeKey(prefix)
	exists, err := s.client.Exists(ctx, nodeKey).Result()
	if err != nil {
		return nil, fmt.Errorf("check trie prefix: %w", err)
	}
	if exists == 0 {
		return nil, nil
	}
	results := make([]string, 0, limit)
	var walk func(string) error
	walk = func(current string) error {
		if len(results) >= limit {
			return nil
		}
		fields, err := s.client.HGetAll(ctx, s.trieNodeKey(current)).Result()
		if err != nil {
			return fmt.Errorf("read trie node: %w", err)
		}
		if _, terminal := fields["_t"]; terminal {
			results = append(results, current)
		}
		children := make([]string, 0, len(fields))
		for field := range fields {
			if strings.HasPrefix(field, "c:") {
				children = append(children, strings.TrimPrefix(field, "c:"))
			}
		}
		sort.Strings(children)
		for _, child := range children {
			if err := walk(current + child); err != nil {
				return err
			}
			if len(results) >= limit {
				return nil
			}
		}
		return nil
	}
	if err := walk(prefix); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *RedisStore) bloomPositions(value string) []uint64 {
	sum := sha256.Sum256([]byte(value))
	h1 := binary.BigEndian.Uint64(sum[0:8])
	h2 := binary.BigEndian.Uint64(sum[8:16])
	if h2 == 0 {
		h2 = 0x9e3779b97f4a7c15
	}
	positions := make([]uint64, 0, s.bloomHashes)
	for i := uint64(0); i < s.bloomHashes; i++ {
		positions = append(positions, (h1+i*h2)%s.bloomBits)
	}
	return positions
}

func bloomShape(expected uint64, falsePositive float64) (uint64, uint64) {
	if expected == 0 {
		expected = 1000000
	}
	if falsePositive <= 0 || falsePositive >= 1 {
		falsePositive = 0.01
	}
	m := -float64(expected) * math.Log(falsePositive) / (math.Ln2 * math.Ln2)
	k := math.Ceil((m / float64(expected)) * math.Ln2)
	if m < 1 {
		m = 1
	}
	if k < 4 {
		k = 4
	}
	return uint64(m), uint64(k)
}

func (s *RedisStore) flushNamespace(ctx context.Context) error {
	iter := s.client.Scan(ctx, 0, namespace+":*", 5000).Iterator()
	keys := make([]string, 0, 5000)
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
		if len(keys) >= 5000 {
			if err := s.client.Del(ctx, keys...).Err(); err != nil {
				return fmt.Errorf("delete redis namespace chunk: %w", err)
			}
			keys = keys[:0]
		}
	}
	if err := iter.Err(); err != nil {
		return fmt.Errorf("scan redis namespace: %w", err)
	}
	if len(keys) > 0 {
		if err := s.client.Del(ctx, keys...).Err(); err != nil {
			return fmt.Errorf("delete redis namespace tail: %w", err)
		}
	}
	return nil
}

func (s *RedisStore) bloomKey() string {
	return namespace + ":bloom"
}

func (s *RedisStore) bloomMetaKey() string {
	return namespace + ":bloom:meta"
}

func (s *RedisStore) bitmapKey(town string) string {
	return fmt.Sprintf("%s:bitmap:%s", namespace, strings.ToUpper(town))
}

func (s *RedisStore) trieNodeKey(prefix string) string {
	if prefix == "" {
		return namespace + ":trie:root"
	}
	return fmt.Sprintf("%s:trie:%s", namespace, sanitizeRedisKey(prefix))
}

func sanitizeRedisKey(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, ":", "-")
	return value
}
