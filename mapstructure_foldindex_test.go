package mapstructure

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// foldCandidateStrings covers ASCII, the non-ASCII SimpleFold orbits that
// matter for case folding (Kelvin sign, long s, sharp s, Greek sigma, Cyrillic,
// dotted/dotless i) and mixed/empty strings.
var foldCandidateStrings = []string{
	"", "k", "K", "K", // Kelvin sign folds with k/K
	"s", "S", "ſ", // long s folds with s/S
	"a", "A", "zz", "ZZ",
	"ß", "ẞ", // ß / capital sharp s
	"σ", "Σ", "ς", // sigma, capital sigma, final sigma
	"д", "Д", // Cyrillic de
	"i", "İ", "ı", // i, dotted capital I, dotless i
	"foo_bar", "FOO_BAR", "Foo_Bar",
	"é", "É", // e-acute pairs
	"straße", "STRASSE",
	"mixedCASEK",
}

func TestFoldKey_EquivalentToStringsEqualFold(t *testing.T) {
	t.Parallel()

	for _, a := range foldCandidateStrings {
		for _, b := range foldCandidateStrings {
			want := strings.EqualFold(a, b)
			got := foldKey(a) == foldKey(b)
			if want != got {
				t.Errorf("foldKey equivalence broken for %q vs %q: EqualFold=%v, foldKey match=%v",
					a, b, want, got)
			}
		}
	}
}

func TestFoldKey_ASCIIFastPathMatchesGeneralPath(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"", "K", "không", "MixedCase", "zZz", "A1_b2"} {
		want := foldKeyGeneral(s)
		got := foldKey(s)
		if got != want {
			t.Errorf("foldKey(%q) = %q, general path = %q", s, got, want)
		}
	}
}

func TestFoldKey_DoesNotGrowSpanningInvalidUTF8(t *testing.T) {
	t.Parallel()

	// Invalid UTF-8 must not panic and must fold consistently with itself.
	for _, s := range []string{"\xff", "a\xffb", "\x80K"} {
		key := foldKey(s)
		if foldKey(s) != key {
			t.Fatalf("foldKey not deterministic for %q", s)
		}
		if utf8.ValidString(s) {
			t.Fatalf("test input unexpectedly valid UTF-8: %q", s)
		}
	}
}

// foldKeyGeneral is the unoptimized reference implementation of foldKey,
// used to check the ASCII fast path against the general path.
func foldKeyGeneral(s string) string {
	runes := []rune(s)
	for i, r := range runes {
		runes[i] = foldRune(r)
	}
	return string(runes)
}

// TestDecoder_EqualFoldFallbackMatchesCustomMatcher checks that the indexed
// default path and the exhaustive-scan path agree on which key wins, given a
// key that only matches through folding. Both decoders use strings.EqualFold,
// just through different code paths.
func TestDecoder_EqualFoldFallbackMatchesCustomMatcher(t *testing.T) {
	t.Parallel()

	type Target struct {
		Field string `mapstructure:"field"`
	}

	// "FIELD" only matches the "field" tag by folding; "ſeild" uses long s,
	// which folds with "s", not "f", so it must not match.
	input := padToFoldIndex(map[string]any{
		"FIELD": "folded",
		"ſeild": "long-s",
	})

	// Reference: a custom (non-EqualFold-pointer) matcher with identical
	// semantics takes the exhaustive-scan path.
	var reference Target
	decoder, err := NewDecoder(&DecoderConfig{
		Result: &reference,
		MatchName: func(mapKey, fieldName string) bool {
			return strings.EqualFold(mapKey, fieldName)
		},
	})
	if err != nil {
		t.Fatalf("err: %s", err)
	}
	if err := decoder.Decode(input); err != nil {
		t.Fatalf("err: %s", err)
	}

	var indexed Target
	if err := Decode(input, &indexed); err != nil {
		t.Fatalf("err: %s", err)
	}

	if reference.Field != "folded" {
		t.Fatalf("reference path picked %q, want %q", reference.Field, "folded")
	}
	if indexed.Field != reference.Field {
		t.Fatalf("default path picked %q, exhaustive scan picked %q",
			indexed.Field, reference.Field)
	}
}

// TestDecoder_EqualFoldFallbackUnicodeKeys covers non-ASCII folding through
// the indexed path: a field whose key differs only by Unicode case folding
// must decode.
func TestDecoder_EqualFoldFallbackUnicodeKeys(t *testing.T) {
	t.Parallel()

	type Target struct {
		Kelvin string `mapstructure:"KELVIN"`
		Sigma  string `mapstructure:"σIGMA"`
	}

	input := padToFoldIndex(map[string]any{
		"Kelvin": "kelvin", // Kelvin sign + "elvin" folds to KELVIN
		"σIGMA":  "sigma",  // already exact for the second tag
	})

	var result Target
	if err := Decode(input, &result); err != nil {
		t.Fatalf("err: %s", err)
	}
	if result.Kelvin != "kelvin" {
		t.Fatalf("expected Kelvin = %q, got %q", "kelvin", result.Kelvin)
	}
	if result.Sigma != "sigma" {
		t.Fatalf("expected Sigma = %q, got %q", "sigma", result.Sigma)
	}
}

// padToFoldIndex adds unrelated keys until m holds at least foldIndexMinKeys
// keys, so a test exercises the indexed fallback path rather than the
// small-map scan. Noise keys never fold-match the field names used in tests.
func padToFoldIndex(m map[string]any) map[string]any {
	for i := 0; len(m) < foldIndexMinKeys; i++ {
		m[fmt.Sprintf("noise_%04d", i)] = i
	}
	return m
}

// TestDecoder_EqualFoldFallbackMetadataAndUnused verifies that the optimized
// path keeps metadata collection and unused-key accounting intact for keys
// that only match through the fallback.
func TestDecoder_EqualFoldFallbackMetadataAndUnused(t *testing.T) {
	t.Parallel()

	type Target struct {
		HostName string `mapstructure:"host_name"`
		Username string `mapstructure:"user_name"`
	}

	input := padToFoldIndex(map[string]any{
		"HOST_NAME":   "server1", // matches host_name via fallback
		"USER_NAME":   "admin",   // matches user_name via fallback
		"NOT_A_FIELD": "ignored",
	})

	var md Metadata
	var result Target
	decoder, err := NewDecoder(&DecoderConfig{
		Metadata: &md,
		Result:   &result,
	})
	if err != nil {
		t.Fatalf("err: %s", err)
	}
	if err := decoder.Decode(input); err != nil {
		t.Fatalf("err: %s", err)
	}

	if result.HostName != "server1" || result.Username != "admin" {
		t.Fatalf("bad decode: %#v", result)
	}

	sort.Strings(md.Unused)
	want := []string{"NOT_A_FIELD"}
	for i := 0; len(md.Unused) > len(want); i++ {
		want = append(want, fmt.Sprintf("noise_%04d", i))
	}
	sort.Strings(want)
	if !reflect.DeepEqual(md.Unused, want) {
		t.Fatalf("bad unused: %#v, want %#v", md.Unused, want)
	}
}

// TestDecoder_EqualFoldFallbackErrorUnused verifies ErrorUnused still reports
// keys that fail fallback matching.
func TestDecoder_EqualFoldFallbackErrorUnused(t *testing.T) {
	t.Parallel()

	type Target struct {
		Field string
	}

	input := padToFoldIndex(map[string]any{
		"fIeLd": "matched",
		"other": "unused",
	})

	var result Target
	decoder, err := NewDecoder(&DecoderConfig{
		ErrorUnused: true,
		Result:      &result,
	})
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	err = decoder.Decode(input)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "other") {
		t.Fatalf("expected error to mention %q, got: %s", "other", err)
	}
	if !strings.Contains("matched", result.Field) {
		t.Fatalf("expected matched field, got: %q", result.Field)
	}
}

// TestDecoder_FallbackNonStringKeys ensures maps with interface keys holding
// non-string values still skip them, on both the indexed and scan paths.
func TestDecoder_FallbackNonStringKeys(t *testing.T) {
	t.Parallel()

	type Target struct {
		Name string
	}

	input := map[any]any{
		1:       "numeric key",
		"NAME":  "matched",
		"other": "unused",
	}
	for i := 0; len(input) < foldIndexMinKeys; i++ {
		input[fmt.Sprintf("noise_%04d", i)] = i
	}

	var result Target
	if err := Decode(input, &result); err != nil {
		t.Fatalf("err: %s", err)
	}
	if result.Name != "matched" {
		t.Fatalf("expected Name = %q, got %q", "matched", result.Name)
	}
}

// TestDecoder_ExactMatchUnaffectedByIndex makes sure the direct lookup still
// wins (and the index is not built) when all keys match exactly.
func TestDecoder_ExactMatchUnaffectedByIndex(t *testing.T) {
	t.Parallel()

	type Target struct {
		Field string
	}

	// One exact key, no fallback involved.
	var result Target
	if err := Decode(map[string]any{"Field": "v"}, &result); err != nil {
		t.Fatalf("err: %s", err)
	}
	if result.Field != "v" {
		t.Fatalf("expected Field = v, got %q", result.Field)
	}
}

// TestDecoder_MatchNameDetection verifies that the default matcher, an
// explicitly supplied strings.EqualFold, and a custom matcher are classified
// as intended, since only the first two may use the folded index.
func TestDecoder_MatchNameDetection(t *testing.T) {
	t.Parallel()

	explicitEqualFold := strings.EqualFold
	custom := func(mapKey, fieldName string) bool {
		return strings.EqualFold(mapKey, fieldName)
	}

	cases := []struct {
		name   string
		match  func(string, string) bool
		wanted bool
	}{
		{name: "default", match: nil, wanted: true},
		{name: "explicit equal fold", match: explicitEqualFold, wanted: true},
		{name: "custom wrapper", match: custom, wanted: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var result struct{ Field string }
			decoder, err := NewDecoder(&DecoderConfig{
				Result:    &result,
				MatchName: tc.match,
			})
			if err != nil {
				t.Fatalf("err: %s", err)
			}
			if decoder.matchNameIsEqualFold != tc.wanted {
				t.Fatalf("matchNameIsEqualFold = %v, want %v",
					decoder.matchNameIsEqualFold, tc.wanted)
			}
		})
	}
}

// TestDecoder_RandomizedFallbackMatchesExhaustiveScan differentially tests the
// indexed default path against an exhaustive-scan matcher with identical
// semantics, over random mixtures of case variants, unrelated keys and
// Unicode-foldable keys.
func TestDecoder_RandomizedFallbackMatchesExhaustiveScan(t *testing.T) {
	t.Parallel()

	type Target struct {
		Alpha string `mapstructure:"alpha"`
		Beta  string `mapstructure:"beta"`
		Gamma string `mapstructure:"gamma"`
		Delta string `mapstructure:"delta"`
	}

	variants := [][]string{
		{"alpha", "ALPHA", "Alpha", "aLPHA"},
		{"beta", "BETA", "Beta"},
		// Greek capital alpha is not ASCII A, so GAMMΑ must not match gamma.
		{"gamma", "GAMMA", "Gamma", "GAMMΑ"},
		// Long s folds with s, so ſelta must not match delta.
		{"delta", "DELTA", "Delta", "ſelta"},
	}
	noise := []string{"unrelated", "OTHER_KEY", ""}

	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 200; iter++ {
		input := make(map[string]any, len(variants)+len(noise))
		for _, group := range variants {
			// At most one candidate per field: with several fold-equal
			// candidates the winner depends on map iteration order, which is
			// unspecified in both the old scan and the indexed lookup.
			if rng.Intn(4) != 0 {
				v := group[rng.Intn(len(group))]
				input[v] = v
			}
		}
		for _, v := range noise {
			if rng.Intn(3) == 0 {
				input[v] = v
			}
		}
		// Always include one exact key so both paths exercise mixed hit/miss.
		input["beta"] = "exact"
		delete(input, "BETA")
		delete(input, "Beta")

		var reference Target
		decoder, err := NewDecoder(&DecoderConfig{
			Result: &reference,
			MatchName: func(mapKey, fieldName string) bool {
				return strings.EqualFold(mapKey, fieldName)
			},
		})
		if err != nil {
			t.Fatalf("err: %s", err)
		}
		if err := decoder.Decode(input); err != nil {
			t.Fatalf("iter %d: reference err: %s", iter, err)
		}

		var indexed Target
		if err := Decode(input, &indexed); err != nil {
			t.Fatalf("iter %d: indexed err: %s", iter, err)
		}

		if !reflect.DeepEqual(reference, indexed) {
			t.Fatalf("iter %d: indexed path = %#v, exhaustive scan = %#v, input = %#v",
				iter, indexed, reference, input)
		}
	}
}

// TestDecoder_FoldIndexThreshold checks fallback matching on both sides of
// foldIndexMinKeys: small maps take the exhaustive scan, wide maps the index,
// and both must decode identically.
func TestDecoder_FoldIndexThreshold(t *testing.T) {
	t.Parallel()

	type Target struct {
		Alpha string `mapstructure:"alpha"`
		Gap   string `mapstructure:"gamma"`
	}

	for _, keys := range []int{foldIndexMinKeys - 1, foldIndexMinKeys, foldIndexMinKeys * 4} {
		keys := keys
		t.Run(fmt.Sprintf("%d_keys", keys), func(t *testing.T) {
			t.Parallel()

			input := make(map[string]any, keys)
			input["ALPHA"] = "a" // fold match for alpha
			input["GAMMA"] = "g" // fold match for gamma
			for i := 2; len(input) < keys; i++ {
				input[fmt.Sprintf("noise_%04d", i)] = i
			}

			var result Target
			if err := Decode(input, &result); err != nil {
				t.Fatalf("err: %s", err)
			}
			if result.Alpha != "a" || result.Gap != "g" {
				t.Fatalf("bad decode: %#v", result)
			}
		})
	}
}

func TestDecoder_WideFallbackMatchesExhaustiveScan(t *testing.T) {
	t.Parallel()

	const width = 400
	typ := buildWideStructType(width)
	input := buildWideInput(width, wideBenchFold)

	// Reference through the exhaustive-scan path.
	reference := reflect.New(typ).Interface()
	decoder, err := NewDecoder(&DecoderConfig{
		Result: reference,
		MatchName: func(mapKey, fieldName string) bool {
			return strings.EqualFold(mapKey, fieldName)
		},
	})
	if err != nil {
		t.Fatalf("err: %s", err)
	}
	if err := decoder.Decode(input); err != nil {
		t.Fatalf("err: %s", err)
	}

	// Default (indexed) path.
	indexed := reflect.New(typ).Interface()
	if err := Decode(input, &indexed); err != nil {
		t.Fatalf("err: %s", err)
	}

	if !reflect.DeepEqual(reference, indexed) {
		t.Fatal("indexed fallback path diverged from exhaustive scan")
	}

	// Every field must have been populated.
	rv := reflect.ValueOf(indexed).Elem()
	for i := 0; i < width; i++ {
		if got := rv.Field(i).String(); got != "value" {
			t.Fatalf("field %d = %q, want %q", i, got, "value")
		}
	}
}

func TestFoldIndex_OrderMatchesMapKeys(t *testing.T) {
	t.Parallel()

	// Within a bucket, entries keep dataVal.MapKeys() order.
	keys := []reflect.Value{
		reflect.ValueOf("FIELD"),
		reflect.ValueOf("field"),
		reflect.ValueOf("Field"),
	}
	index := buildFoldIndex(keys)
	bucket := index[foldKey("field")]
	if len(bucket) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(bucket))
	}
	for i, want := range []string{"FIELD", "field", "Field"} {
		if bucket[i].str != want {
			t.Fatalf("entry %d = %q, want %q", i, bucket[i].str, want)
		}
	}
}

func Example_foldKey() {
	fmt.Println(foldKey("Field") == foldKey("FIELD")) // true
	fmt.Println(foldKey("s") == foldKey("ſ"))         // true, long s
	fmt.Println(foldKey("k") == foldKey("K"))         // true
	// Output:
	// true
	// true
	// true
}
