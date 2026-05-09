package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"demo-number-plates/services/go-api/internal/model"
	"demo-number-plates/services/go-api/internal/storage"
)

var (
	standardSanitized = regexp.MustCompile(`^(?:N)?([1-9][0-9]{0,5})([A-Z]{1,3})$`)
	vanityRaw         = regexp.MustCompile(`^([A-Z0-9]{1,7})(?:\s+NA)?$`)
)

type QueryService struct {
	db    *storage.PostgresStore
	redis *storage.RedisStore
}

func NewQueryService(db *storage.PostgresStore, redis *storage.RedisStore) *QueryService {
	return &QueryService{db: db, redis: redis}
}

func (s *QueryService) Lookup(ctx context.Context, request model.LookupRequest) (*model.LookupResult, *model.APIError) {
	normalized, apiErr := normalizeLookup(request)
	if apiErr != nil {
		return nil, apiErr
	}

	result := &model.LookupResult{
		Input:      request.Query,
		Type:       normalized.Type,
		Normalized: normalized.DisplayPlate,
	}

	bloomHit, err := s.redis.BloomMightContain(ctx, normalized.CanonicalKey)
	if err != nil {
		return nil, &model.APIError{Code: 500, Message: fmt.Sprintf("bloom lookup failed: %v", err)}
	}
	if !bloomHit {
		result.Available = true
		return result, nil
	}

	switch normalized.Type {
	case model.PlateTypeStandard:
		_, err = s.redis.StandardBitmapContains(ctx, normalized.TownCode, normalized.SerialNumber)
	case model.PlateTypeVanity:
		_, err = s.redis.VanityTrieContains(ctx, normalized.VanityText)
		if err == nil && request.SuggestionLimit > 0 && normalized.VanityText != "" {
			prefixLength := min(len(normalized.VanityText), 3)
			result.Suggestions, _ = s.redis.SuggestVanity(ctx, normalized.VanityText[:prefixLength], request.SuggestionLimit)
		}
	}
	if err != nil {
		return nil, &model.APIError{Code: 500, Message: fmt.Sprintf("accelerator lookup failed: %v", err)}
	}

	exists, err := s.db.ExistsCanonical(ctx, normalized.CanonicalKey)
	if err != nil {
		return nil, &model.APIError{Code: 500, Message: fmt.Sprintf("database lookup failed: %v", err)}
	}
	result.Available = !exists
	return result, nil
}

func normalizeLookup(request model.LookupRequest) (model.NormalizedPlate, *model.APIError) {
	queryType := strings.ToLower(strings.TrimSpace(request.Type))
	switch queryType {
	case "std", "standard":
		return normalizeStandard(request.Query)
	case "vty", "vanity":
		return normalizeVanity(request.Query)
	default:
		return model.NormalizedPlate{}, &model.APIError{Code: 400, Message: "parameter t must be std|standard or vty|vanity"}
	}
}

func normalizeStandard(query string) (model.NormalizedPlate, *model.APIError) {
	cleaned := strings.ToUpper(strings.TrimSpace(query))
	cleaned = strings.ReplaceAll(cleaned, "-", "")
	cleaned = strings.ReplaceAll(cleaned, " ", "")
	matched := standardSanitized.FindStringSubmatch(cleaned)
	if len(matched) != 3 {
		return model.NormalizedPlate{}, &model.APIError{Code: 400, Message: "invalid standard plate format"}
	}
	serialValue := 0
	for _, r := range matched[1] {
		serialValue = serialValue*10 + int(r-'0')
	}
	if serialValue < 1 || serialValue > 999999 {
		return model.NormalizedPlate{}, &model.APIError{Code: 400, Message: "standard serial must be between 1 and 999999"}
	}
	townCode := matched[2]
	return model.NormalizedPlate{
		Type:         model.PlateTypeStandard,
		CanonicalKey: formatStandardCanonical(townCode, serialValue),
		DisplayPlate: formatStandardDisplay(townCode, serialValue),
		TownCode:     townCode,
		SerialNumber: serialValue,
	}, nil
}

func normalizeVanity(query string) (model.NormalizedPlate, *model.APIError) {
	cleaned := strings.ToUpper(strings.Join(strings.Fields(query), " "))
	matched := vanityRaw.FindStringSubmatch(cleaned)
	if len(matched) != 2 {
		return model.NormalizedPlate{}, &model.APIError{Code: 400, Message: "invalid vanity plate format"}
	}
	vanity := matched[1]
	return model.NormalizedPlate{
		Type:         model.PlateTypeVanity,
		CanonicalKey: formatVanityCanonical(vanity),
		DisplayPlate: formatVanityDisplay(vanity),
		VanityText:   vanity,
	}, nil
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
