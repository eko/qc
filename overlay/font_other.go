//go:build !darwin

package overlay

// DefaultFont is the overlay's font family: DejaVu Sans Mono, the
// monospaced font of most Linux distributions (fonts-dejavu-mono on
// Debian, installed in qc's Docker images). fontconfig substitutes another
// font when it is missing.
const DefaultFont = "DejaVu Sans Mono"
