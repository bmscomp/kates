package kafkaversion

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in      string
		want    Version
		wantErr bool
	}{
		{in: "4.3.0", want: Version{4, 3, 0}},
		{in: "v4.3.0", want: Version{4, 3, 0}},
		{in: "  4.2.1\n", want: Version{4, 2, 1}},
		{in: "2.1.0", want: Version{2, 1, 0}},
		{in: "10.20.30", want: Version{10, 20, 30}},
		{in: "0.51.0", want: Version{0, 51, 0}},
		{in: "", wantErr: true},
		{in: "4.3", wantErr: true},
		{in: "4", wantErr: true},
		{in: "4.3.0.1", wantErr: true},
		{in: "4.3.x", wantErr: true},
		{in: "4.-3.0", wantErr: true},
		{in: "+4.3.0", wantErr: true},
		{in: "4.3.0-rc1", wantErr: true},
		{in: "4..0", wantErr: true},
		{in: "latest", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := Parse(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) = %v, want error", tt.in, got)
				}
				if !errors.Is(err, ErrInvalidVersion) {
					t.Fatalf("Parse(%q) error %v does not wrap ErrInvalidVersion", tt.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("Parse(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestMustParse(t *testing.T) {
	if got := MustParse("4.3.0"); got != (Version{4, 3, 0}) {
		t.Fatalf("MustParse = %v", got)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("MustParse(\"nope\") did not panic")
		}
	}()
	MustParse("nope")
}

func TestStringAndMajorMinor(t *testing.T) {
	tests := []struct {
		v          Version
		str, short string
	}{
		{Version{4, 3, 0}, "4.3.0", "4.3"},
		{Version{4, 2, 1}, "4.2.1", "4.2"},
		{Version{0, 51, 0}, "0.51.0", "0.51"},
		{Version{10, 0, 7}, "10.0.7", "10.0"},
	}
	for _, tt := range tests {
		if got := tt.v.String(); got != tt.str {
			t.Errorf("%v.String() = %q, want %q", tt.v, got, tt.str)
		}
		if got := tt.v.MajorMinor(); got != tt.short {
			t.Errorf("%v.MajorMinor() = %q, want %q", tt.v, got, tt.short)
		}
	}
}

func TestCompareLessAtLeast(t *testing.T) {
	tests := []struct {
		a, b Version
		cmp  int
	}{
		{Version{4, 3, 0}, Version{4, 3, 0}, 0},
		{Version{4, 2, 1}, Version{4, 3, 0}, -1},
		{Version{4, 3, 0}, Version{4, 2, 1}, 1},
		{Version{4, 2, 0}, Version{4, 2, 1}, -1},
		{Version{3, 9, 1}, Version{4, 0, 0}, -1},
		{Version{4, 10, 0}, Version{4, 9, 9}, 1},
		{Version{1, 1, 0}, Version{0, 51, 0}, 1},
	}
	for _, tt := range tests {
		if got := Compare(tt.a, tt.b); got != tt.cmp {
			t.Errorf("Compare(%v, %v) = %d, want %d", tt.a, tt.b, got, tt.cmp)
		}
		if got := Compare(tt.b, tt.a); got != -tt.cmp {
			t.Errorf("Compare(%v, %v) = %d, want %d", tt.b, tt.a, got, -tt.cmp)
		}
		if got := tt.a.Less(tt.b); got != (tt.cmp < 0) {
			t.Errorf("%v.Less(%v) = %v", tt.a, tt.b, got)
		}
		if got := tt.a.AtLeast(tt.b); got != (tt.cmp >= 0) {
			t.Errorf("%v.AtLeast(%v) = %v", tt.a, tt.b, got)
		}
	}
}

func TestSameMinor(t *testing.T) {
	if !MustParse("4.2.0").SameMinor(MustParse("4.2.1")) {
		t.Error("4.2.0 and 4.2.1 should share a minor line")
	}
	if MustParse("4.2.0").SameMinor(MustParse("4.3.0")) {
		t.Error("4.2.0 and 4.3.0 should not share a minor line")
	}
	if MustParse("3.2.0").SameMinor(MustParse("4.2.0")) {
		t.Error("3.2.0 and 4.2.0 should not share a minor line")
	}
}
