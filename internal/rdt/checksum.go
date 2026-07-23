package rdt

import (
	"encoding/binary"
	"hash/crc32"
)

var ieeeTable = crc32.MakeTable(crc32.IEEE)

func ComputeChecksum(data []byte) uint32 {
	if len(data) < HeaderSize {
		return 0
	}
	packet := make([]byte, len(data))
	copy(packet, data)
	binary.BigEndian.PutUint32(packet[17:21], 0)
	return crc32.Checksum(packet, ieeeTable)
}

func VerifyChecksum(data []byte) bool {
	if len(data) < HeaderSize {
		return false
	}
	computed := ComputeChecksum(data)
	stored := binary.BigEndian.Uint32(data[17:21])
	return computed == stored
}
