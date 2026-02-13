package resize

import (
	"testing"
)

func TestParseValue_Percentage(t *testing.T) {
	tests := []struct {
		name    string
		val     string
		ref     int64
		want    int64
		wantErr bool
	}{
		{"20% of 100Gi", "20%", 100 * gib, 20 * gib, false},
		{"50% of 200Gi", "50%", 200 * gib, 100 * gib, false},
		{"0%", "0%", 100 * gib, 0, false},
		{"100%", "100%", 100 * gib, 100 * gib, false},
		{"negative percent", "-1%", 100 * gib, 0, true},
		{"over 100%", "101%", 100 * gib, 0, true},
		{"invalid percent", "abc%", 100 * gib, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseValue(tt.val, tt.ref)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseValue(%q, %d) error = %v, wantErr %v", tt.val, tt.ref, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseValue(%q, %d) = %d, want %d", tt.val, tt.ref, got, tt.want)
			}
		})
	}
}

func TestParseValue_Absolute(t *testing.T) {
	tests := []struct {
		name    string
		val     string
		want    int64
		wantErr bool
	}{
		{"10Gi", "10Gi", 10 * gib, false},
		{"500Mi", "500Mi", 500 * 1024 * 1024, false},
		{"1Ti", "1Ti", 1024 * gib, false},
		{"zero", "0", 0, true},
		{"negative", "-1Gi", 0, true},
		{"invalid", "notasize", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseValue(tt.val, 0) // ref doesn't matter for absolute
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseValue(%q) error = %v, wantErr %v", tt.val, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseValue(%q) = %d, want %d", tt.val, got, tt.want)
			}
		})
	}
}

func TestParseValue_Empty(t *testing.T) {
	_, err := ParseValue("", 100*gib)
	if err == nil {
		t.Fatal("expected error for empty string")
	}
}

func TestCalculateNewSize(t *testing.T) {
	tests := []struct {
		name     string
		current  int64
		increase int64
		limit    int64
		want     int64
	}{
		{
			"basic increase",
			10 * gib, 5 * gib, 100 * gib,
			15 * gib,
		},
		{
			"rounds up to GiB",
			10 * gib, gib + 1, 100 * gib,
			12 * gib, // 10 + 1.000000001 -> ceil to 12
		},
		{
			"capped at limit",
			90 * gib, 20 * gib, 100 * gib,
			100 * gib,
		},
		{
			"already at limit",
			100 * gib, 10 * gib, 100 * gib,
			100 * gib,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateNewSize(tt.current, tt.increase, tt.limit)
			if got != tt.want {
				t.Errorf("CalculateNewSize(%d, %d, %d) = %d, want %d",
					tt.current, tt.increase, tt.limit, got, tt.want)
			}
		})
	}
}
