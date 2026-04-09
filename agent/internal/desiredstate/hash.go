package desiredstate

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"google.golang.org/protobuf/proto"
)

func HashContainer(c *desiredstatepb.Container) (string, error) {
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(c)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func HashTask(task *desiredstatepb.Task) (string, error) {
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(task)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
