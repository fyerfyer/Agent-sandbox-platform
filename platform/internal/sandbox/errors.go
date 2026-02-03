package sandbox

import "errors"

var (
	ErrContainerNotFound = errors.New("container not found")

	ErrContainerStartFailed = errors.New("failed to start container")

	ErrExecFailed = errors.New("exec failed")

	ErrInvalidPath = errors.New("invalid path")

	ErrImagePullFailed = errors.New("failed to pull image")
)
