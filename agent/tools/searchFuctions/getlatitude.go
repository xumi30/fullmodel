package searchFunctions

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/xumi30/fullmodel/utils"
	"github.com/xumi30/fullmodel/utils/logging"
	"io"

	"net/http"
	"net/url"
)

// GeocodingTool implements the Tool interface for converting location names to coordinates
type GeocodingTool struct{}

// NewGeocodingTool creates a new GeocodingTool instance
func NewGeocodingTool() *GeocodingTool {
	return &GeocodingTool{}
}

// Name returns the name of the tool
func (t *GeocodingTool) Name() string {
	return "geocoding"
}

// Description returns a description of what the tool does
func (t *GeocodingTool) Description() string {
	return "Converts a location name (city, address, landmark) into latitude and longitude coordinates. Input should be a place name like 'Beijing', 'New York', or 'Eiffel Tower'. Returns the coordinates and additional location details."
}

// Parameters returns the parameters that the tool accepts
func (t *GeocodingTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"location": map[string]interface{}{
				"type":        "string",
				"description": "The location name to geocode. Must use 'location' as the parameter name. Examples: 'Beijing', 'Times Square', 'Tokyo Tower'",
			},
		},
		"required": []string{"location"},
	}
}

// Run executes the tool with the given input
func (t *GeocodingTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

// Execute executes the tool with the given arguments
func (t *GeocodingTool) Execute(ctx context.Context, args string) (string, error) {
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		logging.Error("Failed to parse geocoding input: %v", err)
		return "", fmt.Errorf("invalid input format: %v", err)
	}

	var location string
	//尝试只要有一个键值对就取出value作为location
	for _, value := range params {
		location = fmt.Sprintf("%v", value)
		break
	}
	if location == "" {
		logging.Error("Location parameter is missing in geocoding input")
		return "", fmt.Errorf("location parameter is required")
	}

	// Build API URL
	url := fmt.Sprintf("https://nominatim.openstreetmap.org/search?q=%s&format=json&limit=1",
		url.QueryEscape(location))

	// Make HTTP request
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	// Add required User-Agent header for Nominatim API
	req.Header.Set("User-Agent", "LLMWeatherTool/1.0")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		logging.Error("Failed to fetch geocoding data: %v", err)
		return "", fmt.Errorf("failed to fetch geocoding data: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logging.Error("Geocoding API returned non-OK status: %d", resp.StatusCode)
		return "", fmt.Errorf("geocoding API returned status: %d", resp.StatusCode)
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logging.Error("Failed to read geocoding response: %v", err)
		return "", fmt.Errorf("failed to read response: %v", err)
	}

	// Parse JSON response
	var results []map[string]interface{}
	if err := json.Unmarshal(body, &results); err != nil {
		logging.Error("Failed to parse geocoding response: %v", err)
		return "", fmt.Errorf("failed to parse geocoding response: %v", err)
	}

	if len(results) == 0 {
		logging.Error("No geocoding results found for location: %s", location)
		return "", fmt.Errorf("no location found for '%s'", location)
	}

	// Extract coordinates from the first result
	result := results[0]
	lat, ok := result["lat"].(string)
	if !ok {
		logging.Error("Invalid latitude in geocoding response for location: %s", location)
		return "", fmt.Errorf("invalid latitude in response")
	}
	lon, ok := result["lon"].(string)
	if !ok {
		logging.Error("Invalid longitude in geocoding response for location: %s", location)
		return "", fmt.Errorf("invalid longitude in response")
	}

	// Format the response as structured JSON
	wresult := map[string]interface{}{
		"latitude":  lat,
		"longitude": lon,
	}

	jsonBytes, err := json.MarshalIndent(wresult, "", "  ")
	if err != nil {
		logging.Error("Failed to marshal geocoding result: %v", err)
		return "", fmt.Errorf("failed to marshal result: %v", err)
	}

	return string(jsonBytes), nil

}

func (t *GeocodingTool) SimpleInfo() map[string]string {
	return utils.SimpleInfoMap(utils.ToolTopicSearch, "将地名、地址或地标文字解析为经纬度坐标（Nominatim）。")
}

func (t *GeocodingTool) Results() map[string]interface{} {
	return map[string]interface{}{
		"type":        "object",
		"description": "Geocoding tool to convert a location name into latitude and longitude coordinates",
		"properties": map[string]interface{}{
			"latitude": map[string]interface{}{
				"type":        "string",
				"description": "The latitude coordinate of the location",
				"example":     "39.9042",
			},
			"longitude": map[string]interface{}{
				"type":        "string",
				"description": "The longitude coordinate of the location",
				"example":     "116.4074",
			},
		},
	}
}
