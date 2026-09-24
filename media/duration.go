package media

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// jsonPrecision is the resolution of JSON durations (one microsecond): finer
// digits are noise from float conversions and only bloat reports.
const jsonPrecision = 1e6

// Duration is a time.Duration serialised in JSON as decimal seconds, which is
// what charting tools and non-Go consumers expect.
type Duration time.Duration

// Seconds builds a Duration from a number of seconds.
func Seconds(
	s float64,
) Duration {
	return Duration(s * float64(time.Second))
}

// Std returns the standard library duration.
func (d Duration) Std() time.Duration {
	return time.Duration(d)
}

// Seconds returns the duration in seconds.
func (d Duration) Seconds() float64 {
	return time.Duration(d).Seconds()
}

// String implements fmt.Stringer.
func (d Duration) String() string {
	return time.Duration(d).String()
}

// MarshalJSON implements json.Marshaler, rounded to the microsecond.
func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("%g", math.Round(d.Seconds()*jsonPrecision)/jsonPrecision)), nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (d *Duration) UnmarshalJSON(
	data []byte,
) error {
	var s float64
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("decode duration: %w", err)
	}

	*d = Seconds(s)

	return nil
}
