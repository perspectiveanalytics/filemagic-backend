package queue

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestConcurrentJobStatusRead(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	q := New(3, func(ctx context.Context, j *Job) error {
		close(entered)
		<-release
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.Start(ctx)
	_, _, err := q.Submit("snapshot-job", "", ConversionImageFormat, "", "", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	stopped := make(chan struct{})
	reading := make(chan struct{})
	go func() {
		defer close(stopped)
		close(reading)
		end := time.Now().Add(50 * time.Millisecond)
		for time.Now().Before(end) {
			j, ok := q.GetJob("snapshot-job")
			if ok {
				runtime.KeepAlive(j.Status)
				runtime.KeepAlive(j.CompletedAt)
				runtime.KeepAlive(j.Error)
			}
		}
	}()
	<-reading
	close(release)
	<-stopped
}
