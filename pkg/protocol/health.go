package protocol

// GET /health response values for the browser field.
const (
	HealthBrowserConnected    = "connected"
	HealthBrowserDisconnected = "disconnected"
	HealthBrowserSkipped      = "skipped"
)

// HealthResult is the JSON body for GET /health.
type HealthResult struct {
	Status  string `json:"status"`
	Browser string `json:"browser"`
}
