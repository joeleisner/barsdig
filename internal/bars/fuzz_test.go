package bars

import (
	"testing"

	"barsdig/internal/synth"
)

func FuzzParse(f *testing.F) {
	f.Add(synth.BARS(entries()))
	f.Add(synth.BARS(nil))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Parse(data)
	})
}
