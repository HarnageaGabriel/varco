// Package version exposes build metadata set by release linker flags.
package version

// These values are replaced by GoReleaser using -ldflags.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// Info is the stable machine-readable version payload.
type Info struct {
	SchemaVersion int    `json:"schema_version"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	Date          string `json:"date"`
}

// Current returns the current build metadata.
func Current() Info {
	return Info{SchemaVersion: 1, Version: Version, Commit: Commit, Date: Date}
}
