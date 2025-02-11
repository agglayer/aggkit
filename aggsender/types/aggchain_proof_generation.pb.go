// Proto definition for AggSender service

// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.3
// 	protoc        v5.29.3
// source: aggchain_proof_generation.proto

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

type LeafType int32

const (
	// Unspecified leaf type.
	LeafType_LEAF_TYPE_UNSPECIFIED LeafType = 0
	// Transfer leaf type.
	LeafType_LEAF_TYPE_TRANSFER LeafType = 1
	// Message leaf type.
	LeafType_LEAF_TYPE_MESSAGE LeafType = 2
)

// Enum value maps for LeafType.
var (
	LeafType_name = map[int32]string{
		0: "LEAF_TYPE_UNSPECIFIED",
		1: "LEAF_TYPE_TRANSFER",
		2: "LEAF_TYPE_MESSAGE",
	}
	LeafType_value = map[string]int32{
		"LEAF_TYPE_UNSPECIFIED": 0,
		"LEAF_TYPE_TRANSFER":    1,
		"LEAF_TYPE_MESSAGE":     2,
	}
)

func (x LeafType) Enum() *LeafType {
	p := new(LeafType)
	*p = x
	return p
}

func (x LeafType) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (LeafType) Descriptor() protoreflect.EnumDescriptor {
	return file_aggchain_proof_generation_proto_enumTypes[0].Descriptor()
}

func (LeafType) Type() protoreflect.EnumType {
	return &file_aggchain_proof_generation_proto_enumTypes[0]
}

func (x LeafType) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use LeafType.Descriptor instead.
func (LeafType) EnumDescriptor() ([]byte, []int) {
	return file_aggchain_proof_generation_proto_rawDescGZIP(), []int{0}
}

// The request message for generating aggchain proof.
type GenerateAggchainProofRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The start block for which the aggchain proof is requested.
	StartBlock uint64 `protobuf:"varint,1,opt,name=start_block,json=startBlock,proto3" json:"start_block,omitempty"`
	// The max end block for which the aggchain proof is requested.
	MaxEndBlock uint64 `protobuf:"varint,2,opt,name=max_end_block,json=maxEndBlock,proto3" json:"max_end_block,omitempty"`
	// L1 Info tree root. (hash)
	L1InfoTreeRootHash []byte `protobuf:"bytes,3,opt,name=l1_info_tree_root_hash,json=l1InfoTreeRootHash,proto3" json:"l1_info_tree_root_hash,omitempty"`
	// L1 Info tree leaf
	L1InfoTreeLeaf *L1InfoTreeLeaf `protobuf:"bytes,4,opt,name=l1_info_tree_leaf,json=l1InfoTreeLeaf,proto3" json:"l1_info_tree_leaf,omitempty"`
	// L1 Info tree proof. ([32]hash)
	L1InfoTreeMerkleProof [][]byte `protobuf:"bytes,5,rep,name=l1_info_tree_merkle_proof,json=l1InfoTreeMerkleProof,proto3" json:"l1_info_tree_merkle_proof,omitempty"`
	// Map of the GER with their inclusion proof. Note: the GER (string) is a base64 encoded string of the GER digest.
	GerInclusionProofs map[string]*InclusionProof `protobuf:"bytes,6,rep,name=ger_inclusion_proofs,json=gerInclusionProofs,proto3" json:"ger_inclusion_proofs,omitempty" protobuf_key:"bytes,1,opt,name=key" protobuf_val:"bytes,2,opt,name=value"`
	// bridge exits
	ImportedBridgeExits []*ImportedBridgeExit `protobuf:"bytes,7,rep,name=imported_bridge_exits,json=importedBridgeExits,proto3" json:"imported_bridge_exits,omitempty"`
	unknownFields       protoimpl.UnknownFields
	sizeCache           protoimpl.SizeCache
}

func (x *GenerateAggchainProofRequest) Reset() {
	*x = GenerateAggchainProofRequest{}
	mi := &file_aggchain_proof_generation_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GenerateAggchainProofRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GenerateAggchainProofRequest) ProtoMessage() {}

func (x *GenerateAggchainProofRequest) ProtoReflect() protoreflect.Message {
	mi := &file_aggchain_proof_generation_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GenerateAggchainProofRequest.ProtoReflect.Descriptor instead.
func (*GenerateAggchainProofRequest) Descriptor() ([]byte, []int) {
	return file_aggchain_proof_generation_proto_rawDescGZIP(), []int{0}
}

func (x *GenerateAggchainProofRequest) GetStartBlock() uint64 {
	if x != nil {
		return x.StartBlock
	}
	return 0
}

func (x *GenerateAggchainProofRequest) GetMaxEndBlock() uint64 {
	if x != nil {
		return x.MaxEndBlock
	}
	return 0
}

func (x *GenerateAggchainProofRequest) GetL1InfoTreeRootHash() []byte {
	if x != nil {
		return x.L1InfoTreeRootHash
	}
	return nil
}

func (x *GenerateAggchainProofRequest) GetL1InfoTreeLeaf() *L1InfoTreeLeaf {
	if x != nil {
		return x.L1InfoTreeLeaf
	}
	return nil
}

func (x *GenerateAggchainProofRequest) GetL1InfoTreeMerkleProof() [][]byte {
	if x != nil {
		return x.L1InfoTreeMerkleProof
	}
	return nil
}

func (x *GenerateAggchainProofRequest) GetGerInclusionProofs() map[string]*InclusionProof {
	if x != nil {
		return x.GerInclusionProofs
	}
	return nil
}

func (x *GenerateAggchainProofRequest) GetImportedBridgeExits() []*ImportedBridgeExit {
	if x != nil {
		return x.ImportedBridgeExits
	}
	return nil
}

// The aggchain proof response message.
type GenerateAggchainProofResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Aggchain proof.
	AggchainProof []byte `protobuf:"bytes,1,opt,name=aggchain_proof,json=aggchainProof,proto3" json:"aggchain_proof,omitempty"`
	// The start block of the aggchain proof.
	StartBlock uint64 `protobuf:"varint,2,opt,name=start_block,json=startBlock,proto3" json:"start_block,omitempty"`
	// The end block of the aggchain proof.
	EndBlock      uint64 `protobuf:"varint,3,opt,name=end_block,json=endBlock,proto3" json:"end_block,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GenerateAggchainProofResponse) Reset() {
	*x = GenerateAggchainProofResponse{}
	mi := &file_aggchain_proof_generation_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GenerateAggchainProofResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GenerateAggchainProofResponse) ProtoMessage() {}

func (x *GenerateAggchainProofResponse) ProtoReflect() protoreflect.Message {
	mi := &file_aggchain_proof_generation_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GenerateAggchainProofResponse.ProtoReflect.Descriptor instead.
func (*GenerateAggchainProofResponse) Descriptor() ([]byte, []int) {
	return file_aggchain_proof_generation_proto_rawDescGZIP(), []int{1}
}

func (x *GenerateAggchainProofResponse) GetAggchainProof() []byte {
	if x != nil {
		return x.AggchainProof
	}
	return nil
}

func (x *GenerateAggchainProofResponse) GetStartBlock() uint64 {
	if x != nil {
		return x.StartBlock
	}
	return 0
}

func (x *GenerateAggchainProofResponse) GetEndBlock() uint64 {
	if x != nil {
		return x.EndBlock
	}
	return 0
}

type InclusionProof struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Siblings.
	Siblings      [][]byte `protobuf:"bytes,1,rep,name=siblings,proto3" json:"siblings,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *InclusionProof) Reset() {
	*x = InclusionProof{}
	mi := &file_aggchain_proof_generation_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *InclusionProof) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*InclusionProof) ProtoMessage() {}

func (x *InclusionProof) ProtoReflect() protoreflect.Message {
	mi := &file_aggchain_proof_generation_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use InclusionProof.ProtoReflect.Descriptor instead.
func (*InclusionProof) Descriptor() ([]byte, []int) {
	return file_aggchain_proof_generation_proto_rawDescGZIP(), []int{2}
}

func (x *InclusionProof) GetSiblings() [][]byte {
	if x != nil {
		return x.Siblings
	}
	return nil
}

type L1InfoTreeLeaf struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// previous block hash of leaf
	PreviousBlockHash []byte `protobuf:"bytes,1,opt,name=previous_block_hash,json=previousBlockHash,proto3" json:"previous_block_hash,omitempty"`
	// block number timestamp
	Timestamp uint64 `protobuf:"varint,2,opt,name=timestamp,proto3" json:"timestamp,omitempty"`
	// mainnet exit root hash
	MainnetExitRootHash []byte `protobuf:"bytes,3,opt,name=mainnet_exit_root_hash,json=mainnetExitRootHash,proto3" json:"mainnet_exit_root_hash,omitempty"`
	// rollup exit root hash
	RollupExitRootHash []byte `protobuf:"bytes,4,opt,name=rollup_exit_root_hash,json=rollupExitRootHash,proto3" json:"rollup_exit_root_hash,omitempty"`
	// global exit root hash
	GlobalExitRootHash []byte `protobuf:"bytes,5,opt,name=global_exit_root_hash,json=globalExitRootHash,proto3" json:"global_exit_root_hash,omitempty"`
	// leaf hash
	LeafHash []byte `protobuf:"bytes,6,opt,name=leaf_hash,json=leafHash,proto3" json:"leaf_hash,omitempty"`
	// leaf index
	L1InfoTreeIndex uint32 `protobuf:"varint,7,opt,name=l1_info_tree_index,json=l1InfoTreeIndex,proto3" json:"l1_info_tree_index,omitempty"`
	unknownFields   protoimpl.UnknownFields
	sizeCache       protoimpl.SizeCache
}

func (x *L1InfoTreeLeaf) Reset() {
	*x = L1InfoTreeLeaf{}
	mi := &file_aggchain_proof_generation_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *L1InfoTreeLeaf) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*L1InfoTreeLeaf) ProtoMessage() {}

func (x *L1InfoTreeLeaf) ProtoReflect() protoreflect.Message {
	mi := &file_aggchain_proof_generation_proto_msgTypes[3]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use L1InfoTreeLeaf.ProtoReflect.Descriptor instead.
func (*L1InfoTreeLeaf) Descriptor() ([]byte, []int) {
	return file_aggchain_proof_generation_proto_rawDescGZIP(), []int{3}
}

func (x *L1InfoTreeLeaf) GetPreviousBlockHash() []byte {
	if x != nil {
		return x.PreviousBlockHash
	}
	return nil
}

func (x *L1InfoTreeLeaf) GetTimestamp() uint64 {
	if x != nil {
		return x.Timestamp
	}
	return 0
}

func (x *L1InfoTreeLeaf) GetMainnetExitRootHash() []byte {
	if x != nil {
		return x.MainnetExitRootHash
	}
	return nil
}

func (x *L1InfoTreeLeaf) GetRollupExitRootHash() []byte {
	if x != nil {
		return x.RollupExitRootHash
	}
	return nil
}

func (x *L1InfoTreeLeaf) GetGlobalExitRootHash() []byte {
	if x != nil {
		return x.GlobalExitRootHash
	}
	return nil
}

func (x *L1InfoTreeLeaf) GetLeafHash() []byte {
	if x != nil {
		return x.LeafHash
	}
	return nil
}

func (x *L1InfoTreeLeaf) GetL1InfoTreeIndex() uint32 {
	if x != nil {
		return x.L1InfoTreeIndex
	}
	return 0
}

// Represents a token bridge exit originating on another network but claimed on
// the current network.
type ImportedBridgeExit struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// / The bridge exit initiated on another network, called the "sending"
	// / network. Need to verify that the destination network matches the
	// / current network, and that the bridge exit is included in an imported
	// / LER
	BridgeExit *BridgeExit `protobuf:"bytes,1,opt,name=bridge_exit,json=bridgeExit,proto3" json:"bridge_exit,omitempty"`
	// / The global index of the imported bridge exit.
	GlobalIndex   *GlobalIndex `protobuf:"bytes,2,opt,name=global_index,json=globalIndex,proto3" json:"global_index,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ImportedBridgeExit) Reset() {
	*x = ImportedBridgeExit{}
	mi := &file_aggchain_proof_generation_proto_msgTypes[4]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ImportedBridgeExit) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ImportedBridgeExit) ProtoMessage() {}

func (x *ImportedBridgeExit) ProtoReflect() protoreflect.Message {
	mi := &file_aggchain_proof_generation_proto_msgTypes[4]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ImportedBridgeExit.ProtoReflect.Descriptor instead.
func (*ImportedBridgeExit) Descriptor() ([]byte, []int) {
	return file_aggchain_proof_generation_proto_rawDescGZIP(), []int{4}
}

func (x *ImportedBridgeExit) GetBridgeExit() *BridgeExit {
	if x != nil {
		return x.BridgeExit
	}
	return nil
}

func (x *ImportedBridgeExit) GetGlobalIndex() *GlobalIndex {
	if x != nil {
		return x.GlobalIndex
	}
	return nil
}

// Represents a token bridge exit from the network.
type BridgeExit struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The type of the leaf.
	LeafType LeafType `protobuf:"varint,1,opt,name=leaf_type,json=leafType,proto3,enum=types.LeafType" json:"leaf_type,omitempty"`
	// Unique ID for the token being transferred.
	TokenInfo *TokenInfo `protobuf:"bytes,2,opt,name=token_info,json=tokenInfo,proto3" json:"token_info,omitempty"`
	// Network which the token is transferred to
	DestinationNetwork uint32 `protobuf:"varint,3,opt,name=destination_network,json=destinationNetwork,proto3" json:"destination_network,omitempty"`
	// Address which will own the received token
	DestinationAddress []byte `protobuf:"bytes,4,opt,name=destination_address,json=destinationAddress,proto3" json:"destination_address,omitempty"`
	// Token amount sent
	Amount string `protobuf:"bytes,5,opt,name=amount,proto3" json:"amount,omitempty"`
	// is metadata hashed
	IsMetadataHashed bool `protobuf:"varint,6,opt,name=is_metadata_hashed,json=isMetadataHashed,proto3" json:"is_metadata_hashed,omitempty"`
	// Metadata for the bridge exit
	Metadata      []byte `protobuf:"bytes,7,opt,name=metadata,proto3" json:"metadata,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *BridgeExit) Reset() {
	*x = BridgeExit{}
	mi := &file_aggchain_proof_generation_proto_msgTypes[5]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *BridgeExit) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*BridgeExit) ProtoMessage() {}

func (x *BridgeExit) ProtoReflect() protoreflect.Message {
	mi := &file_aggchain_proof_generation_proto_msgTypes[5]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use BridgeExit.ProtoReflect.Descriptor instead.
func (*BridgeExit) Descriptor() ([]byte, []int) {
	return file_aggchain_proof_generation_proto_rawDescGZIP(), []int{5}
}

func (x *BridgeExit) GetLeafType() LeafType {
	if x != nil {
		return x.LeafType
	}
	return LeafType_LEAF_TYPE_UNSPECIFIED
}

func (x *BridgeExit) GetTokenInfo() *TokenInfo {
	if x != nil {
		return x.TokenInfo
	}
	return nil
}

func (x *BridgeExit) GetDestinationNetwork() uint32 {
	if x != nil {
		return x.DestinationNetwork
	}
	return 0
}

func (x *BridgeExit) GetDestinationAddress() []byte {
	if x != nil {
		return x.DestinationAddress
	}
	return nil
}

func (x *BridgeExit) GetAmount() string {
	if x != nil {
		return x.Amount
	}
	return ""
}

func (x *BridgeExit) GetIsMetadataHashed() bool {
	if x != nil {
		return x.IsMetadataHashed
	}
	return false
}

func (x *BridgeExit) GetMetadata() []byte {
	if x != nil {
		return x.Metadata
	}
	return nil
}

type GlobalIndex struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// mainnet flag
	MainnetFlag bool `protobuf:"varint,1,opt,name=mainnet_flag,json=mainnetFlag,proto3" json:"mainnet_flag,omitempty"`
	// rollup index
	RollupIndex uint32 `protobuf:"varint,2,opt,name=rollup_index,json=rollupIndex,proto3" json:"rollup_index,omitempty"`
	// leaf index
	LeafIndex     uint32 `protobuf:"varint,3,opt,name=leaf_index,json=leafIndex,proto3" json:"leaf_index,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GlobalIndex) Reset() {
	*x = GlobalIndex{}
	mi := &file_aggchain_proof_generation_proto_msgTypes[6]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GlobalIndex) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GlobalIndex) ProtoMessage() {}

func (x *GlobalIndex) ProtoReflect() protoreflect.Message {
	mi := &file_aggchain_proof_generation_proto_msgTypes[6]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GlobalIndex.ProtoReflect.Descriptor instead.
func (*GlobalIndex) Descriptor() ([]byte, []int) {
	return file_aggchain_proof_generation_proto_rawDescGZIP(), []int{6}
}

func (x *GlobalIndex) GetMainnetFlag() bool {
	if x != nil {
		return x.MainnetFlag
	}
	return false
}

func (x *GlobalIndex) GetRollupIndex() uint32 {
	if x != nil {
		return x.RollupIndex
	}
	return 0
}

func (x *GlobalIndex) GetLeafIndex() uint32 {
	if x != nil {
		return x.LeafIndex
	}
	return 0
}

// Encapsulates the information to uniquely identify a token on the origin
// network.
type TokenInfo struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Network which the token originates from
	OriginNetwork uint32 `protobuf:"varint,1,opt,name=origin_network,json=originNetwork,proto3" json:"origin_network,omitempty"`
	// The address of the token on the origin network
	OriginTokenAddress []byte `protobuf:"bytes,2,opt,name=origin_token_address,json=originTokenAddress,proto3" json:"origin_token_address,omitempty"`
	unknownFields      protoimpl.UnknownFields
	sizeCache          protoimpl.SizeCache
}

func (x *TokenInfo) Reset() {
	*x = TokenInfo{}
	mi := &file_aggchain_proof_generation_proto_msgTypes[7]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *TokenInfo) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TokenInfo) ProtoMessage() {}

func (x *TokenInfo) ProtoReflect() protoreflect.Message {
	mi := &file_aggchain_proof_generation_proto_msgTypes[7]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TokenInfo.ProtoReflect.Descriptor instead.
func (*TokenInfo) Descriptor() ([]byte, []int) {
	return file_aggchain_proof_generation_proto_rawDescGZIP(), []int{7}
}

func (x *TokenInfo) GetOriginNetwork() uint32 {
	if x != nil {
		return x.OriginNetwork
	}
	return 0
}

func (x *TokenInfo) GetOriginTokenAddress() []byte {
	if x != nil {
		return x.OriginTokenAddress
	}
	return nil
}

var File_aggchain_proof_generation_proto protoreflect.FileDescriptor

var file_aggchain_proof_generation_proto_rawDesc = []byte{
	0x0a, 0x1f, 0x61, 0x67, 0x67, 0x63, 0x68, 0x61, 0x69, 0x6e, 0x5f, 0x70, 0x72, 0x6f, 0x6f, 0x66,
	0x5f, 0x67, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x2e, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x12, 0x05, 0x74, 0x79, 0x70, 0x65, 0x73, 0x22, 0xaf, 0x04, 0x0a, 0x1c, 0x47, 0x65, 0x6e,
	0x65, 0x72, 0x61, 0x74, 0x65, 0x41, 0x67, 0x67, 0x63, 0x68, 0x61, 0x69, 0x6e, 0x50, 0x72, 0x6f,
	0x6f, 0x66, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x1f, 0x0a, 0x0b, 0x73, 0x74, 0x61,
	0x72, 0x74, 0x5f, 0x62, 0x6c, 0x6f, 0x63, 0x6b, 0x18, 0x01, 0x20, 0x01, 0x28, 0x04, 0x52, 0x0a,
	0x73, 0x74, 0x61, 0x72, 0x74, 0x42, 0x6c, 0x6f, 0x63, 0x6b, 0x12, 0x22, 0x0a, 0x0d, 0x6d, 0x61,
	0x78, 0x5f, 0x65, 0x6e, 0x64, 0x5f, 0x62, 0x6c, 0x6f, 0x63, 0x6b, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x04, 0x52, 0x0b, 0x6d, 0x61, 0x78, 0x45, 0x6e, 0x64, 0x42, 0x6c, 0x6f, 0x63, 0x6b, 0x12, 0x32,
	0x0a, 0x16, 0x6c, 0x31, 0x5f, 0x69, 0x6e, 0x66, 0x6f, 0x5f, 0x74, 0x72, 0x65, 0x65, 0x5f, 0x72,
	0x6f, 0x6f, 0x74, 0x5f, 0x68, 0x61, 0x73, 0x68, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x12,
	0x6c, 0x31, 0x49, 0x6e, 0x66, 0x6f, 0x54, 0x72, 0x65, 0x65, 0x52, 0x6f, 0x6f, 0x74, 0x48, 0x61,
	0x73, 0x68, 0x12, 0x40, 0x0a, 0x11, 0x6c, 0x31, 0x5f, 0x69, 0x6e, 0x66, 0x6f, 0x5f, 0x74, 0x72,
	0x65, 0x65, 0x5f, 0x6c, 0x65, 0x61, 0x66, 0x18, 0x04, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x15, 0x2e,
	0x74, 0x79, 0x70, 0x65, 0x73, 0x2e, 0x4c, 0x31, 0x49, 0x6e, 0x66, 0x6f, 0x54, 0x72, 0x65, 0x65,
	0x4c, 0x65, 0x61, 0x66, 0x52, 0x0e, 0x6c, 0x31, 0x49, 0x6e, 0x66, 0x6f, 0x54, 0x72, 0x65, 0x65,
	0x4c, 0x65, 0x61, 0x66, 0x12, 0x38, 0x0a, 0x19, 0x6c, 0x31, 0x5f, 0x69, 0x6e, 0x66, 0x6f, 0x5f,
	0x74, 0x72, 0x65, 0x65, 0x5f, 0x6d, 0x65, 0x72, 0x6b, 0x6c, 0x65, 0x5f, 0x70, 0x72, 0x6f, 0x6f,
	0x66, 0x18, 0x05, 0x20, 0x03, 0x28, 0x0c, 0x52, 0x15, 0x6c, 0x31, 0x49, 0x6e, 0x66, 0x6f, 0x54,
	0x72, 0x65, 0x65, 0x4d, 0x65, 0x72, 0x6b, 0x6c, 0x65, 0x50, 0x72, 0x6f, 0x6f, 0x66, 0x12, 0x6d,
	0x0a, 0x14, 0x67, 0x65, 0x72, 0x5f, 0x69, 0x6e, 0x63, 0x6c, 0x75, 0x73, 0x69, 0x6f, 0x6e, 0x5f,
	0x70, 0x72, 0x6f, 0x6f, 0x66, 0x73, 0x18, 0x06, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x3b, 0x2e, 0x74,
	0x79, 0x70, 0x65, 0x73, 0x2e, 0x47, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x41, 0x67, 0x67,
	0x63, 0x68, 0x61, 0x69, 0x6e, 0x50, 0x72, 0x6f, 0x6f, 0x66, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x2e, 0x47, 0x65, 0x72, 0x49, 0x6e, 0x63, 0x6c, 0x75, 0x73, 0x69, 0x6f, 0x6e, 0x50, 0x72,
	0x6f, 0x6f, 0x66, 0x73, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x52, 0x12, 0x67, 0x65, 0x72, 0x49, 0x6e,
	0x63, 0x6c, 0x75, 0x73, 0x69, 0x6f, 0x6e, 0x50, 0x72, 0x6f, 0x6f, 0x66, 0x73, 0x12, 0x4d, 0x0a,
	0x15, 0x69, 0x6d, 0x70, 0x6f, 0x72, 0x74, 0x65, 0x64, 0x5f, 0x62, 0x72, 0x69, 0x64, 0x67, 0x65,
	0x5f, 0x65, 0x78, 0x69, 0x74, 0x73, 0x18, 0x07, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x19, 0x2e, 0x74,
	0x79, 0x70, 0x65, 0x73, 0x2e, 0x49, 0x6d, 0x70, 0x6f, 0x72, 0x74, 0x65, 0x64, 0x42, 0x72, 0x69,
	0x64, 0x67, 0x65, 0x45, 0x78, 0x69, 0x74, 0x52, 0x13, 0x69, 0x6d, 0x70, 0x6f, 0x72, 0x74, 0x65,
	0x64, 0x42, 0x72, 0x69, 0x64, 0x67, 0x65, 0x45, 0x78, 0x69, 0x74, 0x73, 0x1a, 0x5c, 0x0a, 0x17,
	0x47, 0x65, 0x72, 0x49, 0x6e, 0x63, 0x6c, 0x75, 0x73, 0x69, 0x6f, 0x6e, 0x50, 0x72, 0x6f, 0x6f,
	0x66, 0x73, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x12, 0x10, 0x0a, 0x03, 0x6b, 0x65, 0x79, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x03, 0x6b, 0x65, 0x79, 0x12, 0x2b, 0x0a, 0x05, 0x76, 0x61, 0x6c,
	0x75, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x15, 0x2e, 0x74, 0x79, 0x70, 0x65, 0x73,
	0x2e, 0x49, 0x6e, 0x63, 0x6c, 0x75, 0x73, 0x69, 0x6f, 0x6e, 0x50, 0x72, 0x6f, 0x6f, 0x66, 0x52,
	0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x3a, 0x02, 0x38, 0x01, 0x22, 0x84, 0x01, 0x0a, 0x1d, 0x47,
	0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x41, 0x67, 0x67, 0x63, 0x68, 0x61, 0x69, 0x6e, 0x50,
	0x72, 0x6f, 0x6f, 0x66, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x25, 0x0a, 0x0e,
	0x61, 0x67, 0x67, 0x63, 0x68, 0x61, 0x69, 0x6e, 0x5f, 0x70, 0x72, 0x6f, 0x6f, 0x66, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x0c, 0x52, 0x0d, 0x61, 0x67, 0x67, 0x63, 0x68, 0x61, 0x69, 0x6e, 0x50, 0x72,
	0x6f, 0x6f, 0x66, 0x12, 0x1f, 0x0a, 0x0b, 0x73, 0x74, 0x61, 0x72, 0x74, 0x5f, 0x62, 0x6c, 0x6f,
	0x63, 0x6b, 0x18, 0x02, 0x20, 0x01, 0x28, 0x04, 0x52, 0x0a, 0x73, 0x74, 0x61, 0x72, 0x74, 0x42,
	0x6c, 0x6f, 0x63, 0x6b, 0x12, 0x1b, 0x0a, 0x09, 0x65, 0x6e, 0x64, 0x5f, 0x62, 0x6c, 0x6f, 0x63,
	0x6b, 0x18, 0x03, 0x20, 0x01, 0x28, 0x04, 0x52, 0x08, 0x65, 0x6e, 0x64, 0x42, 0x6c, 0x6f, 0x63,
	0x6b, 0x22, 0x2c, 0x0a, 0x0e, 0x49, 0x6e, 0x63, 0x6c, 0x75, 0x73, 0x69, 0x6f, 0x6e, 0x50, 0x72,
	0x6f, 0x6f, 0x66, 0x12, 0x1a, 0x0a, 0x08, 0x73, 0x69, 0x62, 0x6c, 0x69, 0x6e, 0x67, 0x73, 0x18,
	0x01, 0x20, 0x03, 0x28, 0x0c, 0x52, 0x08, 0x73, 0x69, 0x62, 0x6c, 0x69, 0x6e, 0x67, 0x73, 0x22,
	0xc3, 0x02, 0x0a, 0x0e, 0x4c, 0x31, 0x49, 0x6e, 0x66, 0x6f, 0x54, 0x72, 0x65, 0x65, 0x4c, 0x65,
	0x61, 0x66, 0x12, 0x2e, 0x0a, 0x13, 0x70, 0x72, 0x65, 0x76, 0x69, 0x6f, 0x75, 0x73, 0x5f, 0x62,
	0x6c, 0x6f, 0x63, 0x6b, 0x5f, 0x68, 0x61, 0x73, 0x68, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0c, 0x52,
	0x11, 0x70, 0x72, 0x65, 0x76, 0x69, 0x6f, 0x75, 0x73, 0x42, 0x6c, 0x6f, 0x63, 0x6b, 0x48, 0x61,
	0x73, 0x68, 0x12, 0x1c, 0x0a, 0x09, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x18,
	0x02, 0x20, 0x01, 0x28, 0x04, 0x52, 0x09, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70,
	0x12, 0x33, 0x0a, 0x16, 0x6d, 0x61, 0x69, 0x6e, 0x6e, 0x65, 0x74, 0x5f, 0x65, 0x78, 0x69, 0x74,
	0x5f, 0x72, 0x6f, 0x6f, 0x74, 0x5f, 0x68, 0x61, 0x73, 0x68, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0c,
	0x52, 0x13, 0x6d, 0x61, 0x69, 0x6e, 0x6e, 0x65, 0x74, 0x45, 0x78, 0x69, 0x74, 0x52, 0x6f, 0x6f,
	0x74, 0x48, 0x61, 0x73, 0x68, 0x12, 0x31, 0x0a, 0x15, 0x72, 0x6f, 0x6c, 0x6c, 0x75, 0x70, 0x5f,
	0x65, 0x78, 0x69, 0x74, 0x5f, 0x72, 0x6f, 0x6f, 0x74, 0x5f, 0x68, 0x61, 0x73, 0x68, 0x18, 0x04,
	0x20, 0x01, 0x28, 0x0c, 0x52, 0x12, 0x72, 0x6f, 0x6c, 0x6c, 0x75, 0x70, 0x45, 0x78, 0x69, 0x74,
	0x52, 0x6f, 0x6f, 0x74, 0x48, 0x61, 0x73, 0x68, 0x12, 0x31, 0x0a, 0x15, 0x67, 0x6c, 0x6f, 0x62,
	0x61, 0x6c, 0x5f, 0x65, 0x78, 0x69, 0x74, 0x5f, 0x72, 0x6f, 0x6f, 0x74, 0x5f, 0x68, 0x61, 0x73,
	0x68, 0x18, 0x05, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x12, 0x67, 0x6c, 0x6f, 0x62, 0x61, 0x6c, 0x45,
	0x78, 0x69, 0x74, 0x52, 0x6f, 0x6f, 0x74, 0x48, 0x61, 0x73, 0x68, 0x12, 0x1b, 0x0a, 0x09, 0x6c,
	0x65, 0x61, 0x66, 0x5f, 0x68, 0x61, 0x73, 0x68, 0x18, 0x06, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x08,
	0x6c, 0x65, 0x61, 0x66, 0x48, 0x61, 0x73, 0x68, 0x12, 0x2b, 0x0a, 0x12, 0x6c, 0x31, 0x5f, 0x69,
	0x6e, 0x66, 0x6f, 0x5f, 0x74, 0x72, 0x65, 0x65, 0x5f, 0x69, 0x6e, 0x64, 0x65, 0x78, 0x18, 0x07,
	0x20, 0x01, 0x28, 0x0d, 0x52, 0x0f, 0x6c, 0x31, 0x49, 0x6e, 0x66, 0x6f, 0x54, 0x72, 0x65, 0x65,
	0x49, 0x6e, 0x64, 0x65, 0x78, 0x22, 0x7f, 0x0a, 0x12, 0x49, 0x6d, 0x70, 0x6f, 0x72, 0x74, 0x65,
	0x64, 0x42, 0x72, 0x69, 0x64, 0x67, 0x65, 0x45, 0x78, 0x69, 0x74, 0x12, 0x32, 0x0a, 0x0b, 0x62,
	0x72, 0x69, 0x64, 0x67, 0x65, 0x5f, 0x65, 0x78, 0x69, 0x74, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x11, 0x2e, 0x74, 0x79, 0x70, 0x65, 0x73, 0x2e, 0x42, 0x72, 0x69, 0x64, 0x67, 0x65, 0x45,
	0x78, 0x69, 0x74, 0x52, 0x0a, 0x62, 0x72, 0x69, 0x64, 0x67, 0x65, 0x45, 0x78, 0x69, 0x74, 0x12,
	0x35, 0x0a, 0x0c, 0x67, 0x6c, 0x6f, 0x62, 0x61, 0x6c, 0x5f, 0x69, 0x6e, 0x64, 0x65, 0x78, 0x18,
	0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x74, 0x79, 0x70, 0x65, 0x73, 0x2e, 0x47, 0x6c,
	0x6f, 0x62, 0x61, 0x6c, 0x49, 0x6e, 0x64, 0x65, 0x78, 0x52, 0x0b, 0x67, 0x6c, 0x6f, 0x62, 0x61,
	0x6c, 0x49, 0x6e, 0x64, 0x65, 0x78, 0x22, 0xaf, 0x02, 0x0a, 0x0a, 0x42, 0x72, 0x69, 0x64, 0x67,
	0x65, 0x45, 0x78, 0x69, 0x74, 0x12, 0x2c, 0x0a, 0x09, 0x6c, 0x65, 0x61, 0x66, 0x5f, 0x74, 0x79,
	0x70, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0e, 0x32, 0x0f, 0x2e, 0x74, 0x79, 0x70, 0x65, 0x73,
	0x2e, 0x4c, 0x65, 0x61, 0x66, 0x54, 0x79, 0x70, 0x65, 0x52, 0x08, 0x6c, 0x65, 0x61, 0x66, 0x54,
	0x79, 0x70, 0x65, 0x12, 0x2f, 0x0a, 0x0a, 0x74, 0x6f, 0x6b, 0x65, 0x6e, 0x5f, 0x69, 0x6e, 0x66,
	0x6f, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x10, 0x2e, 0x74, 0x79, 0x70, 0x65, 0x73, 0x2e,
	0x54, 0x6f, 0x6b, 0x65, 0x6e, 0x49, 0x6e, 0x66, 0x6f, 0x52, 0x09, 0x74, 0x6f, 0x6b, 0x65, 0x6e,
	0x49, 0x6e, 0x66, 0x6f, 0x12, 0x2f, 0x0a, 0x13, 0x64, 0x65, 0x73, 0x74, 0x69, 0x6e, 0x61, 0x74,
	0x69, 0x6f, 0x6e, 0x5f, 0x6e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x18, 0x03, 0x20, 0x01, 0x28,
	0x0d, 0x52, 0x12, 0x64, 0x65, 0x73, 0x74, 0x69, 0x6e, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x4e, 0x65,
	0x74, 0x77, 0x6f, 0x72, 0x6b, 0x12, 0x2f, 0x0a, 0x13, 0x64, 0x65, 0x73, 0x74, 0x69, 0x6e, 0x61,
	0x74, 0x69, 0x6f, 0x6e, 0x5f, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x04, 0x20, 0x01,
	0x28, 0x0c, 0x52, 0x12, 0x64, 0x65, 0x73, 0x74, 0x69, 0x6e, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x41,
	0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x12, 0x16, 0x0a, 0x06, 0x61, 0x6d, 0x6f, 0x75, 0x6e, 0x74,
	0x18, 0x05, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x61, 0x6d, 0x6f, 0x75, 0x6e, 0x74, 0x12, 0x2c,
	0x0a, 0x12, 0x69, 0x73, 0x5f, 0x6d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0x5f, 0x68, 0x61,
	0x73, 0x68, 0x65, 0x64, 0x18, 0x06, 0x20, 0x01, 0x28, 0x08, 0x52, 0x10, 0x69, 0x73, 0x4d, 0x65,
	0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0x48, 0x61, 0x73, 0x68, 0x65, 0x64, 0x12, 0x1a, 0x0a, 0x08,
	0x6d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0x18, 0x07, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x08,
	0x6d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0x22, 0x72, 0x0a, 0x0b, 0x47, 0x6c, 0x6f, 0x62,
	0x61, 0x6c, 0x49, 0x6e, 0x64, 0x65, 0x78, 0x12, 0x21, 0x0a, 0x0c, 0x6d, 0x61, 0x69, 0x6e, 0x6e,
	0x65, 0x74, 0x5f, 0x66, 0x6c, 0x61, 0x67, 0x18, 0x01, 0x20, 0x01, 0x28, 0x08, 0x52, 0x0b, 0x6d,
	0x61, 0x69, 0x6e, 0x6e, 0x65, 0x74, 0x46, 0x6c, 0x61, 0x67, 0x12, 0x21, 0x0a, 0x0c, 0x72, 0x6f,
	0x6c, 0x6c, 0x75, 0x70, 0x5f, 0x69, 0x6e, 0x64, 0x65, 0x78, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0d,
	0x52, 0x0b, 0x72, 0x6f, 0x6c, 0x6c, 0x75, 0x70, 0x49, 0x6e, 0x64, 0x65, 0x78, 0x12, 0x1d, 0x0a,
	0x0a, 0x6c, 0x65, 0x61, 0x66, 0x5f, 0x69, 0x6e, 0x64, 0x65, 0x78, 0x18, 0x03, 0x20, 0x01, 0x28,
	0x0d, 0x52, 0x09, 0x6c, 0x65, 0x61, 0x66, 0x49, 0x6e, 0x64, 0x65, 0x78, 0x22, 0x64, 0x0a, 0x09,
	0x54, 0x6f, 0x6b, 0x65, 0x6e, 0x49, 0x6e, 0x66, 0x6f, 0x12, 0x25, 0x0a, 0x0e, 0x6f, 0x72, 0x69,
	0x67, 0x69, 0x6e, 0x5f, 0x6e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x0d, 0x52, 0x0d, 0x6f, 0x72, 0x69, 0x67, 0x69, 0x6e, 0x4e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b,
	0x12, 0x30, 0x0a, 0x14, 0x6f, 0x72, 0x69, 0x67, 0x69, 0x6e, 0x5f, 0x74, 0x6f, 0x6b, 0x65, 0x6e,
	0x5f, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x12,
	0x6f, 0x72, 0x69, 0x67, 0x69, 0x6e, 0x54, 0x6f, 0x6b, 0x65, 0x6e, 0x41, 0x64, 0x64, 0x72, 0x65,
	0x73, 0x73, 0x2a, 0x54, 0x0a, 0x08, 0x4c, 0x65, 0x61, 0x66, 0x54, 0x79, 0x70, 0x65, 0x12, 0x19,
	0x0a, 0x15, 0x4c, 0x45, 0x41, 0x46, 0x5f, 0x54, 0x59, 0x50, 0x45, 0x5f, 0x55, 0x4e, 0x53, 0x50,
	0x45, 0x43, 0x49, 0x46, 0x49, 0x45, 0x44, 0x10, 0x00, 0x12, 0x16, 0x0a, 0x12, 0x4c, 0x45, 0x41,
	0x46, 0x5f, 0x54, 0x59, 0x50, 0x45, 0x5f, 0x54, 0x52, 0x41, 0x4e, 0x53, 0x46, 0x45, 0x52, 0x10,
	0x01, 0x12, 0x15, 0x0a, 0x11, 0x4c, 0x45, 0x41, 0x46, 0x5f, 0x54, 0x59, 0x50, 0x45, 0x5f, 0x4d,
	0x45, 0x53, 0x53, 0x41, 0x47, 0x45, 0x10, 0x02, 0x32, 0x7a, 0x0a, 0x14, 0x41, 0x67, 0x67, 0x63,
	0x68, 0x61, 0x69, 0x6e, 0x50, 0x72, 0x6f, 0x6f, 0x66, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65,
	0x12, 0x62, 0x0a, 0x15, 0x47, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x41, 0x67, 0x67, 0x63,
	0x68, 0x61, 0x69, 0x6e, 0x50, 0x72, 0x6f, 0x6f, 0x66, 0x12, 0x23, 0x2e, 0x74, 0x79, 0x70, 0x65,
	0x73, 0x2e, 0x47, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x41, 0x67, 0x67, 0x63, 0x68, 0x61,
	0x69, 0x6e, 0x50, 0x72, 0x6f, 0x6f, 0x66, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x24,
	0x2e, 0x74, 0x79, 0x70, 0x65, 0x73, 0x2e, 0x47, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x41,
	0x67, 0x67, 0x63, 0x68, 0x61, 0x69, 0x6e, 0x50, 0x72, 0x6f, 0x6f, 0x66, 0x52, 0x65, 0x73, 0x70,
	0x6f, 0x6e, 0x73, 0x65, 0x42, 0x2c, 0x5a, 0x2a, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63,
	0x6f, 0x6d, 0x2f, 0x61, 0x67, 0x67, 0x6c, 0x61, 0x79, 0x65, 0x72, 0x2f, 0x61, 0x67, 0x67, 0x6b,
	0x69, 0x74, 0x2f, 0x61, 0x67, 0x67, 0x73, 0x65, 0x6e, 0x64, 0x65, 0x72, 0x2f, 0x74, 0x79, 0x70,
	0x65, 0x73, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_aggchain_proof_generation_proto_rawDescOnce sync.Once
	file_aggchain_proof_generation_proto_rawDescData = file_aggchain_proof_generation_proto_rawDesc
)

func file_aggchain_proof_generation_proto_rawDescGZIP() []byte {
	file_aggchain_proof_generation_proto_rawDescOnce.Do(func() {
		file_aggchain_proof_generation_proto_rawDescData = protoimpl.X.CompressGZIP(file_aggchain_proof_generation_proto_rawDescData)
	})
	return file_aggchain_proof_generation_proto_rawDescData
}

var file_aggchain_proof_generation_proto_enumTypes = make([]protoimpl.EnumInfo, 1)
var file_aggchain_proof_generation_proto_msgTypes = make([]protoimpl.MessageInfo, 9)
var file_aggchain_proof_generation_proto_goTypes = []any{
	(LeafType)(0),                         // 0: types.LeafType
	(*GenerateAggchainProofRequest)(nil),  // 1: types.GenerateAggchainProofRequest
	(*GenerateAggchainProofResponse)(nil), // 2: types.GenerateAggchainProofResponse
	(*InclusionProof)(nil),                // 3: types.InclusionProof
	(*L1InfoTreeLeaf)(nil),                // 4: types.L1InfoTreeLeaf
	(*ImportedBridgeExit)(nil),            // 5: types.ImportedBridgeExit
	(*BridgeExit)(nil),                    // 6: types.BridgeExit
	(*GlobalIndex)(nil),                   // 7: types.GlobalIndex
	(*TokenInfo)(nil),                     // 8: types.TokenInfo
	nil,                                   // 9: types.GenerateAggchainProofRequest.GerInclusionProofsEntry
}
var file_aggchain_proof_generation_proto_depIdxs = []int32{
	4, // 0: types.GenerateAggchainProofRequest.l1_info_tree_leaf:type_name -> types.L1InfoTreeLeaf
	9, // 1: types.GenerateAggchainProofRequest.ger_inclusion_proofs:type_name -> types.GenerateAggchainProofRequest.GerInclusionProofsEntry
	5, // 2: types.GenerateAggchainProofRequest.imported_bridge_exits:type_name -> types.ImportedBridgeExit
	6, // 3: types.ImportedBridgeExit.bridge_exit:type_name -> types.BridgeExit
	7, // 4: types.ImportedBridgeExit.global_index:type_name -> types.GlobalIndex
	0, // 5: types.BridgeExit.leaf_type:type_name -> types.LeafType
	8, // 6: types.BridgeExit.token_info:type_name -> types.TokenInfo
	3, // 7: types.GenerateAggchainProofRequest.GerInclusionProofsEntry.value:type_name -> types.InclusionProof
	1, // 8: types.AggchainProofService.GenerateAggchainProof:input_type -> types.GenerateAggchainProofRequest
	2, // 9: types.AggchainProofService.GenerateAggchainProof:output_type -> types.GenerateAggchainProofResponse
	9, // [9:10] is the sub-list for method output_type
	8, // [8:9] is the sub-list for method input_type
	8, // [8:8] is the sub-list for extension type_name
	8, // [8:8] is the sub-list for extension extendee
	0, // [0:8] is the sub-list for field type_name
}

func init() { file_aggchain_proof_generation_proto_init() }
func file_aggchain_proof_generation_proto_init() {
	if File_aggchain_proof_generation_proto != nil {
		return
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_aggchain_proof_generation_proto_rawDesc,
			NumEnums:      1,
			NumMessages:   9,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_aggchain_proof_generation_proto_goTypes,
		DependencyIndexes: file_aggchain_proof_generation_proto_depIdxs,
		EnumInfos:         file_aggchain_proof_generation_proto_enumTypes,
		MessageInfos:      file_aggchain_proof_generation_proto_msgTypes,
	}.Build()
	File_aggchain_proof_generation_proto = out.File
	file_aggchain_proof_generation_proto_rawDesc = nil
	file_aggchain_proof_generation_proto_goTypes = nil
	file_aggchain_proof_generation_proto_depIdxs = nil
}
