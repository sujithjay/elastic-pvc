package resize

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

// ParseValue parses a value that can be either a percentage (e.g., "20%") or
// an absolute quantity (e.g., "10Gi"). For percentages, referenceBytes is the
// base value to calculate against.
func ParseValue(val string, referenceBytes int64) (int64, error) {
	if val == "" {
		return 0, fmt.Errorf("empty value")
	}

	if strings.HasSuffix(val, "%") {
		return parsePercent(val, referenceBytes)
	}

	qty, err := resource.ParseQuantity(val)
	if err != nil {
		return 0, fmt.Errorf("parsing quantity %q: %w", val, err)
	}
	bytes := qty.Value()
	if bytes <= 0 {
		return 0, fmt.Errorf("value must be positive: %s", val)
	}
	return bytes, nil
}

func parsePercent(val string, referenceBytes int64) (int64, error) {
	pctStr := strings.TrimSuffix(val, "%")
	pct, err := strconv.ParseFloat(pctStr, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing percentage %q: %w", val, err)
	}
	if pct < 0 || pct > 100 {
		return 0, fmt.Errorf("percentage must be between 0 and 100: %s", val)
	}
	return int64(float64(referenceBytes) * pct / 100.0), nil
}

// CalculateNewSize returns the new PVC size in bytes given the current capacity,
// increase amount, and upper limit. The result is rounded up to the nearest GiB
// since EBS volumes are sized in whole GiB.
func CalculateNewSize(currentBytes, increaseBytes, limitBytes int64) int64 {
	newBytes := currentBytes + increaseBytes
	// Round up to nearest GiB
	newBytes = int64(math.Ceil(float64(newBytes)/gib)) * gib
	if newBytes > limitBytes {
		newBytes = limitBytes
	}
	return newBytes
}

const gib = 1 << 30 // 1 GiB in bytes
