package cli

import (
	"encoding/json"
)

// parseMessageData is a helper function to parse message data.
// It converts interface{} data (typically from JSON decoding) into a specific type T.
func parseMessageData[T any](data interface{}) (*T, error) {
	// Convert via JSON to handle map[string]interface{} from decoder
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	var result T
	if err := json.Unmarshal(bytes, &result); err != nil {
		return nil, err
	}

	return &result, nil
}
