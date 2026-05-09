package model

type PlateType string

const (
	PlateTypeStandard PlateType = "standard"
	PlateTypeVanity   PlateType = "vanity"
)

type LookupRequest struct {
	Type            string
	Query           string
	SuggestionLimit int
}

type NormalizedPlate struct {
	Type         PlateType
	CanonicalKey string
	DisplayPlate string
	TownCode     string
	SerialNumber int
	VanityText   string
}

type LookupResult struct {
	Input       string    `json:"input"`
	Type        PlateType `json:"type"`
	Normalized  string    `json:"normalized"`
	Available   bool      `json:"available"`
	Suggestions []string  `json:"suggestions,omitempty"`
}

type APIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	return e.Message
}

type PlateRecord struct {
	CanonicalKey string
	DisplayPlate string
	PlateType    PlateType
	TownCode     string
	SerialNumber int
	VanityText   string
}
