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
	//prevHash, _ := coin.NewHash("4f3a66613fb969d37c2eea34b85c35391969f7dcd2dff6fdb285b0b2a4a671a3")
	ticker := time.NewTicker(10 * time.Second)

freshNonces:
	for {
		/*
			header := coin.Header{
				ParentID:   prevHash,
				Difficulty: 143287,
				Version:    0x00,
			}
		*/

		header, err := getBlockTemplate()
		if err != nil {
			fmt.Println("ERROR:", err)
			return
		}
		fmt.Println("Mining at difficulty:", header.Difficulty)

		for i := uint64(0); i < header.Difficulty; i++ {
			for j := uint64(0); j < header.Difficulty; j++ {
				for k := uint64(0); k < header.Difficulty; k++ {
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
					header.Nonces[1] = j
					header.Nonces[2] = k
					if header.Valid("") == nil {
						fmt.Println("FOUND HEADER:", *header)
						if err = submitBlock(*header, coin.Block("")); err != nil {
							fmt.Println("ERROR:", err)
						} else {
							continue freshNonces
						}
					}
				}
			}
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
	_, err = client.Do(req)

	return err
}

func getBlockTemplate() (*coin.Header, error) {
	url := "http://192.34.61.144:8080/head"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Connection", "close")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	fmt.Println("body json:", string(body))

	header := new(coin.Header)
	err = json.Unmarshal(body, header)

	header.MerkleRoot = sha256.Sum256([]byte(""))
	header.Timestamp = time.Now()

	return header, err
}
