// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package utils

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
)

var PrivateKey = "LS0tLS1CRUdJTiBSU0EgUFJJVkFURSBLRVktLS0tLQpNSUlFb3dJQkFBS0NBUUVBelZadW9iR1lZamY1U216VEljU2drL01Pd3p6ai9BT3g2SEdKTk5zM0tiaUtUYW1lCjMwaEVvbkFHRkdPUDcyaWU3MUZnS1dEQzdDNFFXemg5M002UThXZDZnalZTMm5vTm54RHpPcEo5UjBGclZjcGUKcDdoTTBUMWVqMHE3Q201Ylovc3hrQURFRW1VNUZwNDE5RzNZSlNybjg1VWVQN05BSlRXMnNTK01rbGZCVWpZUwp1WGhkM2FRUTRRYmd1dmpvVTdZS0xrVXEwL2dXSXVJMXp6ZXlaVE9BTjJrbHI0dWNLUWlDZVRLQy9OVXRSalFWCmdGZ0NnQzB0dDdmU1Q4cVRXS2FHQ0JMeEZhUlFFY21oZlVEWkdPeE1JRWpyZzl3Y3lrK1MrYkh0TXRXZW1LQ3gKMXo5LzNvNVJWK0xBV1RMSlRMdElNWWE2NTlwakZhZFAwQkp1aVFJREFRQUJBb0lCQUFRVDBTelVDMW40OFZIegpaUUpreG9rRmtLK2JEZzVuTHVGV0h0SytQeU5Na3k5OVhLYWpwMXcvNC9rRDdKdmxESkhsUUdiMThVenlFYmhUCi8valNYOEd4YTdiNTh0Z2NpQmgyWkZRUGpGNE52Q0ZISEZmUmdySDllVnNZVVd4QXVuOHZOMkhjQ0FpeStwNGEKdkdqTEJSbWcxMXdJaENJZzc3ZEZjS2dtVVBEbmROUTRHNzRHdDBSZDNqV2ZyS2VIVTROYkw3cVJWWnp6Vyt2NQo2eWN4OGZhd0EvOVRKcU9BOHgwUmRLL0pJVGg5Qm9XVFlQUnV4MHZSTTVrVjlmckxCZW5hRjRIWGM0ck41TGtsClplVTQyTzRLZ2dWV0F0T2VoWHZ1c1d2aVRCNmxhL2h0MndmT1hKK1Q5ZVF6eXN0ckIwR0FIQm1PaEw2bTFydTMKN2pzN3hwRUNnWUVBMk1KdnVIUDFpalZydWlmQjZsYXdhbFdLcDZXUTdYU1NYRlJ4QU9FK0dmQTExTXhwQUVUZgp1ME1rc0Q5NUthVlJjS1dNbUtzd0I3elplMTZGMS80WGhyd3M1QUVZb21KcUtQajhZaEFKMFZROEdMNkY5SGhNCkNlWUFLYWRSTlZQNExHZndvaFdJREhNZGROc01uS2VvaWthSzNZV21ucGlVempQeUp3ZDFSdEVDZ1lFQThvS28KQ3AvbzZkSUxiSEFRcDhPZXVBbDlrZ28wTVI3SUxxTnE2VGliNTBPeGV0eGFYeXZhVGdXcm0rSjN4SlBWUmZCMwpZb0tLQllPcVhjYzRtcVFmV1hQNXNwQjlYZmdRdzIycEVaR0NveXVvQUJIcDRmSktTeHk3aVBwT0I1TnFxaHgxCjE0TjZQTGhkVVpseWJodlpvcnA2ODdNbnVyU2MzelEvSFpaNWlqa0NnWUFhd2RXOHRVUElMZFFBaE12aE81WkgKYWd2VnFoQjczM241djhxN1N4SzViUGVZTHl0L0J3Ri9Ra2lUSVNLNXkxaUVTVXRUeFQ0R2xuOWFSVTdNWE9kVwprSUFTSFRpSFF4TEx3QUNYc2xjajZmd0pLZXVyUS9aTytuOW1wT3JYWkdnc1F5Qm5RYlVycEVJc25LV3Y2TnBiClIxMzQvbmlVOTB6WEwzNWk1djdKSVFLQmdRQ01KcXhjNzR1WXpmWWlKaVhKL3NqVWpVK1B2ZXZwMDJOWGFNUVoKb3NpZS84VXJQdnZQY3JXSVQ4aWNuMlllS2wyZ1BOZVNDK1VlU0xpRjErUERvMFFtMjFxY010cnhHckw5Ym51KwpGbjBNTmVleW1xZXpGK2FOd0Q0MWJJcjUzOTFPRUlLZUdYTGtjcHdqMDIySmF2ajlEWTZQRnFQSVNDYzg2NkhxClJKTmJLUUtCZ0MwbGF0VnkvQnRHV2poWTAxdTBTeHVzbVZvLzU5aUIzTkpGQmgyU3RjTmo5SGhwMUZCL1EzRnQKdVNqV0NqV1J0eXY3M0lyZzhzRXE1Z3BuVlR1YWIwb0htR1pqQ3d3bytCK3QwekpSdU5lbG44UitKdkVsamR1Sgp4dVRKRnZqWlNuRUMxOFBXcUxITUUrQzNFOEdCMUZVcjJzYktMTVBIZ3AvbmRGWnppTEoyCi0tLS0tRU5EIFJTQSBQUklWQVRFIEtFWS0tLS0tCg=="
var PublicKey = "LS0tLS1CRUdJTiBSU0EgUFVCTElDIEtFWS0tLS0tCk1JSUJDZ0tDQVFFQXpWWnVvYkdZWWpmNVNtelRJY1Nnay9NT3d6emovQU94NkhHSk5OczNLYmlLVGFtZTMwaEUKb25BR0ZHT1A3MmllNzFGZ0tXREM3QzRRV3poOTNNNlE4V2Q2Z2pWUzJub05ueER6T3BKOVIwRnJWY3BlcDdoTQowVDFlajBxN0NtNWJaL3N4a0FERUVtVTVGcDQxOUczWUpTcm44NVVlUDdOQUpUVzJzUytNa2xmQlVqWVN1WGhkCjNhUVE0UWJndXZqb1U3WUtMa1VxMC9nV0l1STF6emV5WlRPQU4ya2xyNHVjS1FpQ2VUS0MvTlV0UmpRVmdGZ0MKZ0MwdHQ3ZlNUOHFUV0thR0NCTHhGYVJRRWNtaGZVRFpHT3hNSUVqcmc5d2N5aytTK2JIdE10V2VtS0N4MXo5LwozbzVSVitMQVdUTEpUTHRJTVlhNjU5cGpGYWRQMEJKdWlRSURBUUFCCi0tLS0tRU5EIFJTQSBQVUJMSUMgS0VZLS0tLS0K"

// GenerateRSAKey 生成RSA密钥对，返回Base64编码的字符串
func GenerateRSAKey(bits int) (privateKeyStr, publicKeyStr string, err error) {
	// 生成密钥
	privateKey, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return "", "", err
	}

	// 编码私钥
	privateKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	privateBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	}
	privateKeyStr = base64.StdEncoding.EncodeToString(pem.EncodeToMemory(privateBlock))

	// 编码公钥
	publicKeyBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return "", "", err
	}
	publicBlock := &pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: publicKeyBytes,
	}
	publicKeyStr = base64.StdEncoding.EncodeToString(pem.EncodeToMemory(publicBlock))
	//publicKeyStr = string(pem.EncodeToMemory(publicBlock))

	return privateKeyStr, publicKeyStr, nil
}

// StringToPrivateKey 将Base64字符串转换为RSA私钥
func stringToPrivateKey(privateKeyStr string) (*rsa.PrivateKey, error) {
	// Base64解码
	keyBytes, err := base64.StdEncoding.DecodeString(privateKeyStr)
	if err != nil {
		return nil, err
	}

	// PEM解码
	block, _ := pem.Decode(keyBytes)
	if block == nil {
		return nil, errors.New("failed to parse PEM block")
	}

	// 解析私钥
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}

	return privateKey, nil
}

// StringToPublicKey 将Base64字符串转换为RSA公钥
func stringToPublicKey(publicKeyStr string) (*rsa.PublicKey, error) {
	// Base64解码
	keyBytes, err := base64.StdEncoding.DecodeString(publicKeyStr)
	if err != nil {
		return nil, err
	}

	// PEM解码
	block, _ := pem.Decode(keyBytes)
	if block == nil {
		return nil, errors.New("failed to parse PEM block")
	}

	// 解析公钥
	publicKey, err := x509.ParsePKCS1PublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}

	return publicKey, nil
}

// RSAEncrypt RSA加密
func RSAEncrypt(plaintext, publicKeyStr string) (string, error) {
	// 获取公钥
	publicKey, err := stringToPublicKey(publicKeyStr)
	if err != nil {
		return "", err
	}

	// 加密（OAEP）
	ciphertext, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, publicKey, []byte(plaintext), nil)
	if err != nil {
		return "", err
	}

	// Base64编码返回
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// RSADecrypt RSA解密
func RSADecrypt(ciphertext, privateKeyStr string) (string, error) {
	// Base64解码
	ciphertextBytes, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}

	// 获取私钥
	privateKey, err := stringToPrivateKey(privateKeyStr)
	if err != nil {
		return "", err
	}

	// 解密（OAEP）
	plaintext, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, privateKey, ciphertextBytes, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}
