package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/cfromknecht/857coin/coin"
)

func main() {
	//prevHash, _ := coin.NewHash("b4acc61d6bee28979e6a936c89e37b324630c885775eeba36e6ae1ecabad4c13")
	ticker := time.NewTicker(5 * time.Second)

freshNonces:
	for {
		/*
			header := coin.Header{
				ParentID:   prevHash,
				MerkleRoot: sha256.Sum256(nil),
				Difficulty: 10,
				Timestamp:  time.Now().UnixNano(),
				Version:    0x00,
			}
		*/

		header, err := getBlockTemplate()
		if err != nil {
			fmt.Println("ERROR:", err)
			return
		}
		fmt.Println("Mining at difficulty:", header.Difficulty)

		// Calculate modulus
		dInt := new(big.Int).SetUint64(genesisHeader.Difficulty)
		mInt := new(big.Int).SetUint64(2)
		mInt.Exp(mInt, dInt, nil)

		hashMap := make(map[uint64][]uint64)
		i := uint64(0)
		for {
			select {
			case <-ticker.C:
				header, err = getBlockTemplate()
				if err != nil {
					fmt.Println("ERROR:", err)
					return
				}
				fmt.Println("Mining at difficulty:", header.Difficulty)
			default:
				break
			}

			header.Nonces[0] = i
			aHash := header.SumNonce(0)
			aInt := new(big.Int).SetBytes(aHash[:])
			aInt.Mod(aInt, mInt)

			a := aInt.Uint64()
			if ns, ok := hashMap[a]; ok {
				if len(ns) == 2 {
					header.Nonces[0] = ns[0]
					header.Nonces[1] = ns[1]
					header.Nonces[2] = i

					if err := submitBlock(header, b); err != nil {
						panic(err)
					} else {
						continue freshNonces
					}
				}
				hashMap[a] = append(ns, i)
			} else {
				hashMap[a] = []uint64{i}
			}
			i++
		}
	}
}

func submitBlock(h coin.Header, b coin.Block) error {
	url := "http://192.34.61.144:8080/add"

	cblock := struct {
		Header coin.Header `json:"header"`
		Block  coin.Block  `json:"block"`
	}{
		Header: h,
		Block:  b,
	}

	cblockBytes, err := json.Marshal(cblock)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(cblockBytes))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection", "close")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	return resp.Body.Close()
}

func getBlockTemplate() (*coin.Header, error) {
	resp, err := http.Get("http://192.34.61.144:8080/next")
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	header := new(coin.Header)
	err = json.Unmarshal(body, header)

	header.MerkleRoot = sha256.Sum256([]byte(""))
	header.Timestamp = time.Now().UnixNano()

	return header, err
}
