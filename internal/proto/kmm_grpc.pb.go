// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.2.0
// - protoc             v3.19.6
// source: internal/proto/kmm.proto

package proto

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

// ControlPlaneClient is the client API for ControlPlane service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type ControlPlaneClient interface {
	GetDesiredModules(ctx context.Context, in *Node, opts ...grpc.CallOption) (*DesiredModules, error)
}

type controlPlaneClient struct {
	cc grpc.ClientConnInterface
}

func NewControlPlaneClient(cc grpc.ClientConnInterface) ControlPlaneClient {
	return &controlPlaneClient{cc}
}

func (c *controlPlaneClient) GetDesiredModules(ctx context.Context, in *Node, opts ...grpc.CallOption) (*DesiredModules, error) {
	out := new(DesiredModules)
	err := c.cc.Invoke(ctx, "/ControlPlane/GetDesiredModules", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ControlPlaneServer is the server API for ControlPlane service.
// All implementations must embed UnimplementedControlPlaneServer
// for forward compatibility
type ControlPlaneServer interface {
	GetDesiredModules(context.Context, *Node) (*DesiredModules, error)
	mustEmbedUnimplementedControlPlaneServer()
}

// UnimplementedControlPlaneServer must be embedded to have forward compatible implementations.
type UnimplementedControlPlaneServer struct {
}

func (UnimplementedControlPlaneServer) GetDesiredModules(context.Context, *Node) (*DesiredModules, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetDesiredModules not implemented")
}
func (UnimplementedControlPlaneServer) mustEmbedUnimplementedControlPlaneServer() {}

// UnsafeControlPlaneServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to ControlPlaneServer will
// result in compilation errors.
type UnsafeControlPlaneServer interface {
	mustEmbedUnimplementedControlPlaneServer()
}

func RegisterControlPlaneServer(s grpc.ServiceRegistrar, srv ControlPlaneServer) {
	s.RegisterService(&ControlPlane_ServiceDesc, srv)
}

func _ControlPlane_GetDesiredModules_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(Node)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ControlPlaneServer).GetDesiredModules(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/ControlPlane/GetDesiredModules",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ControlPlaneServer).GetDesiredModules(ctx, req.(*Node))
	}
	return interceptor(ctx, in, info, handler)
}

// ControlPlane_ServiceDesc is the grpc.ServiceDesc for ControlPlane service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var ControlPlane_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "ControlPlane",
	HandlerType: (*ControlPlaneServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetDesiredModules",
			Handler:    _ControlPlane_GetDesiredModules_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "internal/proto/kmm.proto",
}
