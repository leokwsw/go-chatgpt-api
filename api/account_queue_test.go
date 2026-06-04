package api

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAcquireAccountRequestSerializesSameAccount(t *testing.T) {
	releaseFirst, err := AcquireAccountRequest(context.Background(), "token-serial")
	if err != nil {
		t.Fatalf("AcquireAccountRequest() first = %v", err)
	}
	defer releaseFirst()

	acquiredSecond := make(chan struct{})
	errCh := make(chan error, 1)

	go func() {
		releaseSecond, err := AcquireAccountRequest(context.Background(), "token-serial")
		if err != nil {
			errCh <- err
			return
		}
		defer releaseSecond()
		close(acquiredSecond)
	}()

	select {
	case <-acquiredSecond:
		t.Fatal("second request acquired the same account before the first released it")
	case err := <-errCh:
		t.Fatalf("AcquireAccountRequest() second = %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	releaseFirst()

	select {
	case <-acquiredSecond:
	case err := <-errCh:
		t.Fatalf("AcquireAccountRequest() second after release = %v", err)
	case <-time.After(1 * time.Second):
		t.Fatal("second request never acquired the account after release")
	}
}

func TestAcquireAccountRequestDoesNotBlockDifferentAccounts(t *testing.T) {
	releaseFirst, err := AcquireAccountRequest(context.Background(), "token-a")
	if err != nil {
		t.Fatalf("AcquireAccountRequest() first = %v", err)
	}
	defer releaseFirst()

	acquiredOther := make(chan struct{})
	errCh := make(chan error, 1)

	go func() {
		releaseOther, err := AcquireAccountRequest(context.Background(), "token-b")
		if err != nil {
			errCh <- err
			return
		}
		defer releaseOther()
		close(acquiredOther)
	}()

	select {
	case <-acquiredOther:
	case err := <-errCh:
		t.Fatalf("AcquireAccountRequest() other = %v", err)
	case <-time.After(300 * time.Millisecond):
		t.Fatal("different accounts should not wait on the same queue")
	}
}

func TestAcquireAccountRequestRespectsContextCancellation(t *testing.T) {
	releaseFirst, err := AcquireAccountRequest(context.Background(), "token-cancel")
	if err != nil {
		t.Fatalf("AcquireAccountRequest() first = %v", err)
	}
	defer releaseFirst()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		releaseSecond, err := AcquireAccountRequest(ctx, "token-cancel")
		if releaseSecond != nil {
			releaseSecond()
		}
		errCh <- err
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("AcquireAccountRequest() cancellation error = %v, want DeadlineExceeded", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("waiting request did not exit after context cancellation")
	}

	releaseFirst()

	releaseThird, err := AcquireAccountRequest(context.Background(), "token-cancel")
	if err != nil {
		t.Fatalf("AcquireAccountRequest() third = %v", err)
	}
	releaseThird()
}
