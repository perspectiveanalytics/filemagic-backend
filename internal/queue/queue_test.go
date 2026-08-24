package queue

import (
	"errors"
	"testing"
)

// The worker is never started here, so submitted jobs stay buffered and keep
// their in-flight slot, which is exactly what the fairness cap counts.
func TestSubmitPerClientCap(t *testing.T) {
	q := New(9, nil) // perClientLimit = 9/3 = 3

	for i := 0; i < 3; i++ {
		if _, _, err := q.Submit("a"+string(rune('0'+i)), "1.1.1.1", ConversionImageFormat, "", "n", nil, 0); err != nil {
			t.Fatalf("submit %d for client A should succeed: %v", i, err)
		}
	}

	// Fourth job from the same client is rejected (reported as a full queue).
	if _, _, err := q.Submit("a3", "1.1.1.1", ConversionImageFormat, "", "n", nil, 0); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("client A over cap should get ErrQueueFull, got %v", err)
	}

	// A different client still gets in.
	if _, _, err := q.Submit("b0", "2.2.2.2", ConversionImageFormat, "", "n", nil, 0); err != nil {
		t.Fatalf("client B should succeed: %v", err)
	}

	// Trusted/internal traffic (empty owner) is exempt from the cap.
	for i := 0; i < 4; i++ {
		if _, _, err := q.Submit("t"+string(rune('0'+i)), "", ConversionImageFormat, "", "n", nil, 0); err != nil {
			t.Fatalf("exempt submit %d should succeed: %v", i, err)
		}
	}

	// Freeing one of client A's slots lets it submit again.
	q.releaseSlot("1.1.1.1")
	if _, _, err := q.Submit("a4", "1.1.1.1", ConversionImageFormat, "", "n", nil, 0); err != nil {
		t.Fatalf("client A after release should succeed: %v", err)
	}
}

func TestSubmitQueueFull(t *testing.T) {
	q := New(2, nil)
	for i := 0; i < 2; i++ {
		if _, _, err := q.Submit("j"+string(rune('0'+i)), "", ConversionImageFormat, "", "n", nil, 0); err != nil {
			t.Fatalf("submit %d should succeed: %v", i, err)
		}
	}
	if _, _, err := q.Submit("j2", "", ConversionImageFormat, "", "n", nil, 0); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("expected ErrQueueFull when queue is full, got %v", err)
	}
}
