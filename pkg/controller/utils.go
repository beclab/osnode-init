package controllers

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

func GenTerminusNonce() (string, error) {
	randomKey := os.Getenv("APP_RANDOM_KEY")
	timestamp := getTimestamp()
	cipherText, err := AesEncrypt([]byte(timestamp), []byte(randomKey))
	if err != nil {
		return "", err
	}
	b64CipherText := base64.StdEncoding.EncodeToString(cipherText)
	terminusNonce := "appservice:" + b64CipherText
	return terminusNonce, nil
}

func getTimestamp() string {
	t := time.Now().Unix()
	return strconv.Itoa(int(t))
}

func AesEncrypt(origin, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	blockSize := block.BlockSize()
	origin = PKCS7Padding(origin, blockSize)
	blockMode := cipher.NewCBCEncrypter(block, key[:blockSize])
	crypted := make([]byte, len(origin))
	blockMode.CryptBlocks(crypted, origin)
	return crypted, nil
}

func PKCS7Padding(ciphertext []byte, blockSize int) []byte {
	padding := blockSize - len(ciphertext)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(ciphertext, padtext...)
}

func ToJSON(v any) string {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		panic(err)
	}
	return buf.String()
}

func getRedisIpAndPassword() (ip string, pwd string, err error) {
	file, err := os.ReadFile("/olares/data/redis/etc/redis.conf")
	if err != nil {
		return
	}

	for _, line := range strings.Split(string(file), "\n") {
		conf := strings.Split(line, " ")
		if len(conf) > 1 {
			switch conf[0] {
			case "requirepass":
				pwd = conf[1]
			case "bind":
				ip = conf[1]
			}
		}
	}

	if ip == "" || pwd == "" {
		err = errors.New("get redis info error")
	}

	return
}
