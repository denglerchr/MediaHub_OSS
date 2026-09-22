package entryhandler

import (
	"encoding/json"
	"fmt"
	"math"
	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/shared/customerrors"
)

// parseUploadMetadata validates the request and parses the 'metadata' JSON part of the POST request.
// It also assigns the current timestamp in case a timestamp was not provided.
func parseUploadMetadata(metadataStr string) (PostPatchEntryRequest, error) {
	var entry = PostPatchEntryRequest{
		FileName:  "",
		Timestamp: math.MinInt64, // default, indicates missing timestamp
	}

	// Parse Metadata
	if metadataStr == "" {
		return entry, fmt.Errorf("%w: missing 'metadata' part in multipart form", customerrors.ErrValidation)
	}

	if err := json.Unmarshal([]byte(metadataStr), &entry); err != nil {
		return entry, fmt.Errorf("%w: invalid JSON in 'metadata' part", customerrors.ErrValidation)
	}

	return entry, nil
}

// ValidateCustomFields checks if the provided fields exist in the database schema
// and if their data types match.
func validateCustomFields(provided map[string]any, defined []repository.CustomFieldDef) error {
	// Create a lookup map for fast checking
	allowedFields := make(map[string]repository.CustomFieldType)
	for _, f := range defined {
		allowedFields[f.Name] = f.Type
	}

	// Validate each provided field
	for key, val := range provided {
		// Check if the field exists in the schema
		fieldType, exists := allowedFields[key]
		if !exists {
			return fmt.Errorf("unknown custom field provided: '%s'", key)
		}

		// Check if the type matches
		switch fieldType {
		case repository.CustomFieldTypeText:
			if _, ok := val.(string); !ok {
				return fmt.Errorf("custom field '%s' must be a string", key)
			}
		case repository.CustomFieldTypeInteger:
			// json.Unmarshal parses all numbers into `any` as `float64`
			num, ok := val.(float64)

			// Check if it's a number AND if it has no fractional part (e.g. 42.0 == 42)
			if !ok || num != float64(int64(num)) {
				return fmt.Errorf("custom field '%s' must be an integer", key)
			}

			// Convert it to an actual int64 in the map so the DB driver gets the right type!
			provided[key] = int64(num)
		case repository.CustomFieldTypeReal:
			if _, ok := val.(float64); !ok {
				return fmt.Errorf("custom field '%s' must be a float", key)
			}
		case repository.CustomFieldTypeBoolean:
			if _, ok := val.(bool); !ok {
				return fmt.Errorf("custom field '%s' must be a boolean", key)
			}
		case repository.CustomFieldTypeCoordinate:
			if val == nil {
				continue
			}
			coord, err := repository.ParseCoordinate(val)
			if err != nil {
				return fmt.Errorf("custom field '%s' must be a valid coordinate: %w", key, err)
			}
			provided[key] = coord
		}
	}

	return nil
}
