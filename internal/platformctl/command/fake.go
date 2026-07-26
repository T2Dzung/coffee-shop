package command

import (
	"context"
	"fmt"
	"sync"
)

type Expectation struct {
	Name   string
	Args   []string
	Result Result
	Err    error
}

type FakeRunner struct {
	mu           sync.Mutex
	Expectations []Expectation
	Requests     []Request
}

func (f *FakeRunner) Run(_ context.Context, request Request) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Requests = append(f.Requests, request)
	index := len(f.Requests) - 1
	if index >= len(f.Expectations) {
		return Result{}, fmt.Errorf("unexpected command %s", request.Name)
	}
	expected := f.Expectations[index]
	if expected.Name != request.Name || !sameStrings(expected.Args, request.Args) {
		return Result{}, fmt.Errorf("command %d: got %s %v, want %s %v", index, request.Name, request.Args, expected.Name, expected.Args)
	}
	return expected.Result, expected.Err
}

func (f *FakeRunner) Verify() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.Requests) != len(f.Expectations) {
		return fmt.Errorf("received %d commands, expected %d", len(f.Requests), len(f.Expectations))
	}
	return nil
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
