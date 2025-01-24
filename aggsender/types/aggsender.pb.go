// Proto definition for AggSender service

// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.3
// 	protoc        v5.29.3
// source: aggsender.proto

package types

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type FetchAuthProofRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The start block for which the auth proof is requested.
	StartBlock uint64 `protobuf:"varint,1,opt,name=start_block,json=startBlock,proto3" json:"start_block,omitempty"`
	// The max end block for which the auth proof is requested.
	MaxEndBlock   uint64 `protobuf:"varint,2,opt,name=max_end_block,json=maxEndBlock,proto3" json:"max_end_block,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *FetchAuthProofRequest) Reset() {
	*x = FetchAuthProofRequest{}
	mi := &file_aggsender_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *FetchAuthProofRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*FetchAuthProofRequest) ProtoMessage() {}

func (x *FetchAuthProofRequest) ProtoReflect() protoreflect.Message {
	mi := &file_aggsender_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use FetchAuthProofRequest.ProtoReflect.Descriptor instead.
func (*FetchAuthProofRequest) Descriptor() ([]byte, []int) {
	return file_aggsender_proto_rawDescGZIP(), []int{0}
}

func (x *FetchAuthProofRequest) GetStartBlock() uint64 {
	if x != nil {
		return x.StartBlock
	}
	return 0
}

func (x *FetchAuthProofRequest) GetMaxEndBlock() uint64 {
	if x != nil {
		return x.MaxEndBlock
	}
	return 0
}

type FetchAuthProofResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The auth proof.
	// TODO - Define the type of auth proof.
	AuthProof     []byte `protobuf:"bytes,1,opt,name=auth_proof,json=authProof,proto3" json:"auth_proof,omitempty"`
	StartBlock    uint64 `protobuf:"varint,2,opt,name=start_block,json=startBlock,proto3" json:"start_block,omitempty"`
	EndBlock      uint64 `protobuf:"varint,3,opt,name=end_block,json=endBlock,proto3" json:"end_block,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *FetchAuthProofResponse) Reset() {
	*x = FetchAuthProofResponse{}
	mi := &file_aggsender_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *FetchAuthProofResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*FetchAuthProofResponse) ProtoMessage() {}

func (x *FetchAuthProofResponse) ProtoReflect() protoreflect.Message {
	mi := &file_aggsender_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use FetchAuthProofResponse.ProtoReflect.Descriptor instead.
func (*FetchAuthProofResponse) Descriptor() ([]byte, []int) {
	return file_aggsender_proto_rawDescGZIP(), []int{1}
}

func (x *FetchAuthProofResponse) GetAuthProof() []byte {
	if x != nil {
		return x.AuthProof
	}
	return nil
}

func (x *FetchAuthProofResponse) GetStartBlock() uint64 {
	if x != nil {
		return x.StartBlock
	}
	return 0
}

func (x *FetchAuthProofResponse) GetEndBlock() uint64 {
	if x != nil {
		return x.EndBlock
	}
	return 0
}

// Error message returned when an error occurs during auth proof request.
type FetchAuthProofError struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The error message as bytes.
	Error         []byte `protobuf:"bytes,1,opt,name=error,proto3" json:"error,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *FetchAuthProofError) Reset() {
	*x = FetchAuthProofError{}
	mi := &file_aggsender_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *FetchAuthProofError) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*FetchAuthProofError) ProtoMessage() {}

func (x *FetchAuthProofError) ProtoReflect() protoreflect.Message {
	mi := &file_aggsender_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use FetchAuthProofError.ProtoReflect.Descriptor instead.
func (*FetchAuthProofError) Descriptor() ([]byte, []int) {
	return file_aggsender_proto_rawDescGZIP(), []int{2}
}

func (x *FetchAuthProofError) GetError() []byte {
	if x != nil {
		return x.Error
	}
	return nil
}

var File_aggsender_proto protoreflect.FileDescriptor

var file_aggsender_proto_rawDesc = []byte{
	0x0a, 0x0f, 0x61, 0x67, 0x67, 0x73, 0x65, 0x6e, 0x64, 0x65, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x12, 0x05, 0x74, 0x79, 0x70, 0x65, 0x73, 0x22, 0x5c, 0x0a, 0x15, 0x46, 0x65, 0x74, 0x63,
	0x68, 0x41, 0x75, 0x74, 0x68, 0x50, 0x72, 0x6f, 0x6f, 0x66, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x12, 0x1f, 0x0a, 0x0b, 0x73, 0x74, 0x61, 0x72, 0x74, 0x5f, 0x62, 0x6c, 0x6f, 0x63, 0x6b,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x04, 0x52, 0x0a, 0x73, 0x74, 0x61, 0x72, 0x74, 0x42, 0x6c, 0x6f,
	0x63, 0x6b, 0x12, 0x22, 0x0a, 0x0d, 0x6d, 0x61, 0x78, 0x5f, 0x65, 0x6e, 0x64, 0x5f, 0x62, 0x6c,
	0x6f, 0x63, 0x6b, 0x18, 0x02, 0x20, 0x01, 0x28, 0x04, 0x52, 0x0b, 0x6d, 0x61, 0x78, 0x45, 0x6e,
	0x64, 0x42, 0x6c, 0x6f, 0x63, 0x6b, 0x22, 0x75, 0x0a, 0x16, 0x46, 0x65, 0x74, 0x63, 0x68, 0x41,
	0x75, 0x74, 0x68, 0x50, 0x72, 0x6f, 0x6f, 0x66, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65,
	0x12, 0x1d, 0x0a, 0x0a, 0x61, 0x75, 0x74, 0x68, 0x5f, 0x70, 0x72, 0x6f, 0x6f, 0x66, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x0c, 0x52, 0x09, 0x61, 0x75, 0x74, 0x68, 0x50, 0x72, 0x6f, 0x6f, 0x66, 0x12,
	0x1f, 0x0a, 0x0b, 0x73, 0x74, 0x61, 0x72, 0x74, 0x5f, 0x62, 0x6c, 0x6f, 0x63, 0x6b, 0x18, 0x02,
	0x20, 0x01, 0x28, 0x04, 0x52, 0x0a, 0x73, 0x74, 0x61, 0x72, 0x74, 0x42, 0x6c, 0x6f, 0x63, 0x6b,
	0x12, 0x1b, 0x0a, 0x09, 0x65, 0x6e, 0x64, 0x5f, 0x62, 0x6c, 0x6f, 0x63, 0x6b, 0x18, 0x03, 0x20,
	0x01, 0x28, 0x04, 0x52, 0x08, 0x65, 0x6e, 0x64, 0x42, 0x6c, 0x6f, 0x63, 0x6b, 0x22, 0x2b, 0x0a,
	0x13, 0x46, 0x65, 0x74, 0x63, 0x68, 0x41, 0x75, 0x74, 0x68, 0x50, 0x72, 0x6f, 0x6f, 0x66, 0x45,
	0x72, 0x72, 0x6f, 0x72, 0x12, 0x14, 0x0a, 0x05, 0x65, 0x72, 0x72, 0x6f, 0x72, 0x18, 0x01, 0x20,
	0x01, 0x28, 0x0c, 0x52, 0x05, 0x65, 0x72, 0x72, 0x6f, 0x72, 0x32, 0x61, 0x0a, 0x10, 0x41, 0x75,
	0x74, 0x68, 0x50, 0x72, 0x6f, 0x6f, 0x66, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x4d,
	0x0a, 0x0e, 0x46, 0x65, 0x74, 0x63, 0x68, 0x41, 0x75, 0x74, 0x68, 0x50, 0x72, 0x6f, 0x6f, 0x66,
	0x12, 0x1c, 0x2e, 0x74, 0x79, 0x70, 0x65, 0x73, 0x2e, 0x46, 0x65, 0x74, 0x63, 0x68, 0x41, 0x75,
	0x74, 0x68, 0x50, 0x72, 0x6f, 0x6f, 0x66, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x1d,
	0x2e, 0x74, 0x79, 0x70, 0x65, 0x73, 0x2e, 0x46, 0x65, 0x74, 0x63, 0x68, 0x41, 0x75, 0x74, 0x68,
	0x50, 0x72, 0x6f, 0x6f, 0x66, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x42, 0x2c, 0x5a,
	0x2a, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x61, 0x67, 0x67, 0x6c,
	0x61, 0x79, 0x65, 0x72, 0x2f, 0x61, 0x67, 0x67, 0x6b, 0x69, 0x74, 0x2f, 0x61, 0x67, 0x67, 0x73,
	0x65, 0x6e, 0x64, 0x65, 0x72, 0x2f, 0x74, 0x79, 0x70, 0x65, 0x73, 0x62, 0x06, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x33,
}

var (
	file_aggsender_proto_rawDescOnce sync.Once
	file_aggsender_proto_rawDescData = file_aggsender_proto_rawDesc
)

func file_aggsender_proto_rawDescGZIP() []byte {
	file_aggsender_proto_rawDescOnce.Do(func() {
		file_aggsender_proto_rawDescData = protoimpl.X.CompressGZIP(file_aggsender_proto_rawDescData)
	})
	return file_aggsender_proto_rawDescData
}

var file_aggsender_proto_msgTypes = make([]protoimpl.MessageInfo, 3)
var file_aggsender_proto_goTypes = []any{
	(*FetchAuthProofRequest)(nil),  // 0: types.FetchAuthProofRequest
	(*FetchAuthProofResponse)(nil), // 1: types.FetchAuthProofResponse
	(*FetchAuthProofError)(nil),    // 2: types.FetchAuthProofError
}
var file_aggsender_proto_depIdxs = []int32{
	0, // 0: types.AuthProofService.FetchAuthProof:input_type -> types.FetchAuthProofRequest
	1, // 1: types.AuthProofService.FetchAuthProof:output_type -> types.FetchAuthProofResponse
	1, // [1:2] is the sub-list for method output_type
	0, // [0:1] is the sub-list for method input_type
	0, // [0:0] is the sub-list for extension type_name
	0, // [0:0] is the sub-list for extension extendee
	0, // [0:0] is the sub-list for field type_name
}

func init() { file_aggsender_proto_init() }
func file_aggsender_proto_init() {
	if File_aggsender_proto != nil {
		return
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_aggsender_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   3,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_aggsender_proto_goTypes,
		DependencyIndexes: file_aggsender_proto_depIdxs,
		MessageInfos:      file_aggsender_proto_msgTypes,
	}.Build()
	File_aggsender_proto = out.File
	file_aggsender_proto_rawDesc = nil
	file_aggsender_proto_goTypes = nil
	file_aggsender_proto_depIdxs = nil
}
