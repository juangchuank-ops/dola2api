package dola

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// randomUUID returns a RFC 4122 v4 UUID string.
func randomUUID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		now := time.Now().UnixNano()
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", now>>32, (now>>16)&0xffff, (now)&0xffff, now&0xffff, now)
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(buf[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

var crc32Table = func() [256]uint32 {
	var table [256]uint32
	for n := 0; n < 256; n++ {
		c := uint32(n)
		for k := 0; k < 8; k++ {
			if c&1 == 1 {
				c = 0xEDB88320 ^ (c >> 1)
			} else {
				c >>= 1
			}
		}
		table[n] = c
	}
	return table
}()

// crc32Hex computes the CRC32 checksum ImageX expects in Content-CRC32.
func crc32Hex(data []byte) string {
	c := ^uint32(0)
	for _, b := range data {
		c = crc32Table[(c^uint32(b))&0xff] ^ (c >> 8)
	}
	return fmt.Sprintf("%08x", ^c)
}
