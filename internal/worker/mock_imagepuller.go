// Code generated by MockGen. DO NOT EDIT.
// Source: imagepuller.go

// Package worker is a generated GoMock package.
package worker

import (
	context "context"
	reflect "reflect"

	gomock "github.com/golang/mock/gomock"
)

// MockImagePuller is a mock of ImagePuller interface.
type MockImagePuller struct {
	ctrl     *gomock.Controller
	recorder *MockImagePullerMockRecorder
}

// MockImagePullerMockRecorder is the mock recorder for MockImagePuller.
type MockImagePullerMockRecorder struct {
	mock *MockImagePuller
}

// NewMockImagePuller creates a new mock instance.
func NewMockImagePuller(ctrl *gomock.Controller) *MockImagePuller {
	mock := &MockImagePuller{ctrl: ctrl}
	mock.recorder = &MockImagePullerMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockImagePuller) EXPECT() *MockImagePullerMockRecorder {
	return m.recorder
}

// PullAndExtract mocks base method.
func (m *MockImagePuller) PullAndExtract(ctx context.Context, imageName string, insecurePull bool) (PullResult, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "PullAndExtract", ctx, imageName, insecurePull)
	ret0, _ := ret[0].(PullResult)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// PullAndExtract indicates an expected call of PullAndExtract.
func (mr *MockImagePullerMockRecorder) PullAndExtract(ctx, imageName, insecurePull interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "PullAndExtract", reflect.TypeOf((*MockImagePuller)(nil).PullAndExtract), ctx, imageName, insecurePull)
}
