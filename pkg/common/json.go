package common

import "encoding/json"

// JSONMarshal wraps json.Marshal for consistent error handling
func JSONMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// JSONUnmarshal wraps json.Unmarshal for consistent error handling
func JSONUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
