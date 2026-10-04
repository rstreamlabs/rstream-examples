package webrtc

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/logs"
)

func TestKeyFrameRequestRetainsALaterReaderInsideTheRateLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		encoder := &recordingKeyFrameRequester{requested: make(chan struct{}, 4)}
		session := &Session{encoder: encoder, logger: logs.NewLogger(logs.NewHub(8), false), closed: make(chan struct{})}
		defer session.Close("test complete")
		session.requestKeyFrame()
		<-encoder.requested
		// The previous key frame can precede a new reader's arrival. Suppressing
		// its PLI entirely leaves it waiting for the next periodic GOP.
		time.Sleep(keyFrameRequestInterval / 2)
		session.requestKeyFrame()
		session.requestKeyFrame()
		time.Sleep(keyFrameRequestInterval/2 - time.Nanosecond)
		synctest.Wait()
		if len(encoder.requested) != 0 {
			t.Fatal("reader request bypassed the key-frame rate limit")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if len(encoder.requested) != 1 {
			t.Fatal("the coalesced reader request was lost")
		}
		<-encoder.requested
		time.Sleep(2 * keyFrameRequestInterval)
		synctest.Wait()
		if len(encoder.requested) != 0 {
			t.Fatal("key-frame requests continued without reader demand")
		}
	})
}

func TestKeyFrameRequestCancelsALaterReaderWhenTheSessionCloses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		encoder := &recordingKeyFrameRequester{requested: make(chan struct{}, 4)}
		session := &Session{encoder: encoder, logger: logs.NewLogger(logs.NewHub(8), false), closed: make(chan struct{})}
		session.requestKeyFrame()
		<-encoder.requested
		session.requestKeyFrame()
		session.Close("reader left")
		time.Sleep(2 * keyFrameRequestInterval)
		synctest.Wait()
		if len(encoder.requested) != 0 {
			t.Fatal("reader request survived session closure")
		}
	})
}
