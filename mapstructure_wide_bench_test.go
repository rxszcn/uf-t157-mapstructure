package mapstructure

import (
	"fmt"
	"reflect"
	"testing"
)

// wideBenchWidths is the width ladder used by Benchmark_DecodeWide. The
// fallback key matching path used to be quadratic in width, so the ladder
// doubles to make the growth visible.
var wideBenchWidths = []int{250, 500, 1000, 2000, 4000}

// buildWideStructType builds a struct type with "width" string fields named
// Field0000..FieldNNNN, each tagged with an upper-case snake-ish key
// (FIELD0000..FIELDNNNN).
func buildWideStructType(width int) reflect.Type {
	fields := make([]reflect.StructField, width)
	for i := 0; i < width; i++ {
		fields[i] = reflect.StructField{
			Name: fmt.Sprintf("Field%04d", i),
			Type: reflect.TypeOf(""),
			Tag:  reflect.StructTag(fmt.Sprintf(`mapstructure:"FIELD%04d"`, i)),
		}
	}
	return reflect.StructOf(fields)
}

// wideBenchMode selects the shape of the input keys:
//   - exact: keys are identical to the field tags, so the fast direct lookup hits
//   - fold: keys differ only in case, so every field falls through to MatchName
//   - miss: keys never match any field, the worst case for the fallback scan
type wideBenchMode string

const (
	wideBenchExact wideBenchMode = "exact"
	wideBenchFold  wideBenchMode = "fold"
	wideBenchMiss  wideBenchMode = "miss"
)

func buildWideInput(width int, mode wideBenchMode) map[string]any {
	input := make(map[string]any, width)
	for i := 0; i < width; i++ {
		var key string
		switch mode {
		case wideBenchExact:
			key = fmt.Sprintf("FIELD%04d", i)
		case wideBenchFold:
			// Same letters as the tag, lower case.
			key = fmt.Sprintf("field%04d", i)
		case wideBenchMiss:
			key = fmt.Sprintf("key%04d", i)
		}
		input[key] = "value"
	}
	return input
}

// Benchmark_DecodeWide measures decoding a width-N map onto a width-N struct
// for the three key shapes above.
func Benchmark_DecodeWide(b *testing.B) {
	for _, width := range wideBenchWidths {
		width := width
		typ := buildWideStructType(width)
		for _, mode := range []wideBenchMode{wideBenchExact, wideBenchFold, wideBenchMiss} {
			mode := mode
			input := buildWideInput(width, mode)
			b.Run(fmt.Sprintf("%s/%d", mode, width), func(b *testing.B) {
				result := reflect.New(typ)
				decoder, err := NewDecoder(&DecoderConfig{Result: result.Interface()})
				if err != nil {
					b.Fatalf("NewDecoder: %v", err)
				}
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if err := decoder.Decode(input); err != nil {
						b.Fatalf("Decode: %v", err)
					}
				}
			})
		}
	}
}

// Benchmark_DecodeWideCustomMatchName mirrors the fold case but with a
// user-supplied (rather than default) case-insensitive predicate, to show that
// custom matchers keep their exact existing behaviour.
func Benchmark_DecodeWideCustomMatchName(b *testing.B) {
	for _, width := range wideBenchWidths {
		width := width
		typ := buildWideStructType(width)
		input := buildWideInput(width, wideBenchFold)
		b.Run(fmt.Sprintf("custom-fold/%d", width), func(b *testing.B) {
			result := reflect.New(typ)
			decoder, err := NewDecoder(&DecoderConfig{
				Result: result.Interface(),
				MatchName: func(mapKey, fieldName string) bool {
					return equalFoldASCII(mapKey, fieldName)
				},
			})
			if err != nil {
				b.Fatalf("NewDecoder: %v", err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := decoder.Decode(input); err != nil {
					b.Fatalf("Decode: %v", err)
				}
			}
		})
	}
}

// equalFoldASCII is an ASCII-only case-insensitive comparison used by the
// custom MatchName benchmark. It is deliberately a plain function, not
// strings.EqualFold, so it exercises the custom matcher path.
func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
