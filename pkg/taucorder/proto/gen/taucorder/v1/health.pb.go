// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.34.2
// 	protoc        (unknown)
// source: taucorder/v1/health.proto

package taucorderv1

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

var File_taucorder_v1_health_proto protoreflect.FileDescriptor

var file_taucorder_v1_health_proto_rawDesc = []byte{
	0x0a, 0x19, 0x74, 0x61, 0x75, 0x63, 0x6f, 0x72, 0x64, 0x65, 0x72, 0x2f, 0x76, 0x31, 0x2f, 0x68,
	0x65, 0x61, 0x6c, 0x74, 0x68, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x0c, 0x74, 0x61, 0x75,
	0x63, 0x6f, 0x72, 0x64, 0x65, 0x72, 0x2e, 0x76, 0x31, 0x1a, 0x19, 0x74, 0x61, 0x75, 0x63, 0x6f,
	0x72, 0x64, 0x65, 0x72, 0x2f, 0x76, 0x31, 0x2f, 0x63, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x2e, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x32, 0x41, 0x0a, 0x0d, 0x48, 0x65, 0x61, 0x6c, 0x74, 0x68, 0x53, 0x65,
	0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x30, 0x0a, 0x04, 0x50, 0x69, 0x6e, 0x67, 0x12, 0x13, 0x2e,
	0x74, 0x61, 0x75, 0x63, 0x6f, 0x72, 0x64, 0x65, 0x72, 0x2e, 0x76, 0x31, 0x2e, 0x45, 0x6d, 0x70,
	0x74, 0x79, 0x1a, 0x13, 0x2e, 0x74, 0x61, 0x75, 0x63, 0x6f, 0x72, 0x64, 0x65, 0x72, 0x2e, 0x76,
	0x31, 0x2e, 0x45, 0x6d, 0x70, 0x74, 0x79, 0x42, 0xb9, 0x01, 0x0a, 0x10, 0x63, 0x6f, 0x6d, 0x2e,
	0x74, 0x61, 0x75, 0x63, 0x6f, 0x72, 0x64, 0x65, 0x72, 0x2e, 0x76, 0x31, 0x42, 0x0b, 0x48, 0x65,
	0x61, 0x6c, 0x74, 0x68, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x47, 0x67, 0x69, 0x74,
	0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x74, 0x61, 0x75, 0x62, 0x79, 0x74, 0x65, 0x2f,
	0x74, 0x61, 0x75, 0x2f, 0x70, 0x6b, 0x67, 0x2f, 0x74, 0x61, 0x75, 0x63, 0x6f, 0x72, 0x64, 0x65,
	0x72, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x67, 0x65, 0x6e, 0x2f, 0x74, 0x61, 0x75, 0x63,
	0x6f, 0x72, 0x64, 0x65, 0x72, 0x2f, 0x76, 0x31, 0x3b, 0x74, 0x61, 0x75, 0x63, 0x6f, 0x72, 0x64,
	0x65, 0x72, 0x76, 0x31, 0xa2, 0x02, 0x03, 0x54, 0x58, 0x58, 0xaa, 0x02, 0x0c, 0x54, 0x61, 0x75,
	0x63, 0x6f, 0x72, 0x64, 0x65, 0x72, 0x2e, 0x56, 0x31, 0xca, 0x02, 0x0c, 0x54, 0x61, 0x75, 0x63,
	0x6f, 0x72, 0x64, 0x65, 0x72, 0x5c, 0x56, 0x31, 0xe2, 0x02, 0x18, 0x54, 0x61, 0x75, 0x63, 0x6f,
	0x72, 0x64, 0x65, 0x72, 0x5c, 0x56, 0x31, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64,
	0x61, 0x74, 0x61, 0xea, 0x02, 0x0d, 0x54, 0x61, 0x75, 0x63, 0x6f, 0x72, 0x64, 0x65, 0x72, 0x3a,
	0x3a, 0x56, 0x31, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var file_taucorder_v1_health_proto_goTypes = []any{
	(*Empty)(nil), // 0: taucorder.v1.Empty
}
var file_taucorder_v1_health_proto_depIdxs = []int32{
	0, // 0: taucorder.v1.HealthService.Ping:input_type -> taucorder.v1.Empty
	0, // 1: taucorder.v1.HealthService.Ping:output_type -> taucorder.v1.Empty
	1, // [1:2] is the sub-list for method output_type
	0, // [0:1] is the sub-list for method input_type
	0, // [0:0] is the sub-list for extension type_name
	0, // [0:0] is the sub-list for extension extendee
	0, // [0:0] is the sub-list for field type_name
}

func init() { file_taucorder_v1_health_proto_init() }
func file_taucorder_v1_health_proto_init() {
	if File_taucorder_v1_health_proto != nil {
		return
	}
	file_taucorder_v1_common_proto_init()
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_taucorder_v1_health_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   0,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_taucorder_v1_health_proto_goTypes,
		DependencyIndexes: file_taucorder_v1_health_proto_depIdxs,
	}.Build()
	File_taucorder_v1_health_proto = out.File
	file_taucorder_v1_health_proto_rawDesc = nil
	file_taucorder_v1_health_proto_goTypes = nil
	file_taucorder_v1_health_proto_depIdxs = nil
}
