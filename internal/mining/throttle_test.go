package mining

import (
	"strconv"
	"testing"
)

// TestBlockThrottleDivisorOne verifies throttling is disabled with a divisor
// of 1.
func TestBlockThrottleDivisorOne(t *testing.T) {
	throttle := NewBlockThrottle(1)
	for i := 0; i < 10; i++ {
		if !throttle.Allow() {
			t.Fatalf("iteration %d: divisor 1 must always allow", i)
		}
	}
	found, submitted, throttled := throttle.Stats()
	if found != 10 || submitted != 10 || throttled != 0 {
		t.Fatalf("stats got found=%d submitted=%d throttled=%d", found, submitted, throttled)
	}
}

// TestBlockThrottleDivisorZero verifies a zero divisor is treated as
// disabled.
func TestBlockThrottleDivisorZero(t *testing.T) {
	throttle := NewBlockThrottle(0)
	if !throttle.Allow() {
		t.Fatal("divisor 0 must behave as disabled")
	}
	if throttle.Divisor() != 1 {
		t.Fatalf("divisor got %d want 1", throttle.Divisor())
	}
}

// TestBlockThrottleSubmitEveryN verifies that with a divisor of N, only 1 in
// every N solved blocks is submitted, starting from the Nth block.
func TestBlockThrottleSubmitEveryN(t *testing.T) {
	tests := []struct {
		divisor    uint32
		iterations int
		// wantSubmissions[i] is whether iteration i (0-indexed) is submitted.
		wantSubmissions []bool
	}{
		{
			divisor:    2,
			iterations: 6,
			// found blocks 2, 4, 6 are submitted (1 in 2).
			wantSubmissions: []bool{false, true, false, true, false, true},
		},
		{
			divisor:    3,
			iterations: 9,
			// found blocks 3, 6, 9 are submitted (1 in 3).
			wantSubmissions: []bool{false, false, true, false, false, true, false, false, true},
		},
		{
			divisor:    10,
			iterations: 20,
			// found blocks 10, 20 are submitted (1 in 10).
			wantSubmissions: []bool{false, false, false, false, false, false, false, false,
				false, true, false, false, false, false, false, false, false, false, false, true},
		},
	}

	for _, tc := range tests {
		name := "divisor_" + strconv.Itoa(int(tc.divisor))
		t.Run(name, func(t *testing.T) {
			throttle := NewBlockThrottle(tc.divisor)
			var submittedCount, throttledCount int
			for i := 0; i < tc.iterations; i++ {
				allowed := throttle.Allow()
				want := tc.wantSubmissions[i]
				if allowed != want {
					t.Fatalf("iteration %d: Allow got %v want %v", i, allowed, want)
				}
				if allowed {
					submittedCount++
				} else {
					throttledCount++
				}
			}

			found, submitted, throttled := throttle.Stats()
			if int(found) != tc.iterations {
				t.Fatalf("found got %d want %d", found, tc.iterations)
			}
			if int(submitted) != submittedCount || int(throttled) != throttledCount {
				t.Fatalf("stats got submitted=%d throttled=%d want submitted=%d throttled=%d",
					submitted, throttled, submittedCount, throttledCount)
			}

			// Verify the submitted count is floor(iterations/divisor).
			wantSubmitted := tc.iterations / int(tc.divisor)
			if submittedCount != wantSubmitted {
				t.Fatalf("submitted got %d want %d", submittedCount, wantSubmitted)
			}
		})
	}
}
