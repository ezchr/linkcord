//go:build no_voice

package voice

import "errors"

func StartMediaAudio(fill func([]byte)) (func(), error) {
	return nil, errors.New("audio is disabled in this build")
}

func StartLoopback(data func(pcm []byte)) (func(), error) {
	return nil, errors.New("audio is disabled in this build")
}
