package ids

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
)

const roomCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func NewID(prefix string) string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic(fmt.Sprintf("generate id: %v", err))
	}

	return prefix + "_" + hex.EncodeToString(bytes)
}

func NewToken() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		panic(fmt.Sprintf("generate token: %v", err))
	}

	return hex.EncodeToString(bytes)
}

func NewRoomCode(existing map[string]struct{}) string {
	for {
		code := make([]byte, 6)
		for i := range code {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(roomCodeAlphabet))))
			if err != nil {
				panic(fmt.Sprintf("generate room code: %v", err))
			}
			code[i] = roomCodeAlphabet[n.Int64()]
		}

		value := string(code)
		if _, ok := existing[value]; !ok {
			return value
		}
	}
}
