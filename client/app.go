package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"syscall"
	"time"

	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"

	"golang.org/x/crypto/scrypt"
	"golang.org/x/term"
)

var serverAddr string

func init() {
	flag.StringVar(&serverAddr, "addr", "", "HTTP address of the server.")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s --addr <server_address>\n", os.Args[0])
		flag.PrintDefaults()
	}
}

func main() {
	flag.Parse()
	if serverAddr == "" {
		flag.Usage()
		os.Exit(1)
	}

	var op int
	fmt.Printf("1) Add new password\n2) Get password\n3) Delete password\nDo: ")
	_, err := fmt.Scanf("%d", &op)
	if err != nil {
		log.Fatal(err)
	}

	switch op {
	case 1:
		addSecret(serverAddr)
	case 2:
		getSecret(serverAddr)
	// TODO
	//case 3:
	//	delSecret(serverAddr)
	default:
		log.Fatal(fmt.Errorf("Error: Unknown operation %d\n", op))
	}

}

type Secret struct {
	Identifier    string `json:"key"`
	EncryptedText string `json:"value"`
}

type serverResponse struct {
	Data    Secret  `json:"data"`
	Message *string `json:"message"`
	Error   *string `json:"error"`
}

func getSecret(serverAddr string) {
	var secretId string
	fmt.Printf("(1/2) Enter identifier: ")
	fmt.Scan(&secretId)

	// Request content from server using identifier from above
	requestURL := fmt.Sprintf("http://%s/db/%s", serverAddr, secretId)
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		log.Fatalf("error creating http request: %s\n", err)
	}

	client := http.Client{
		Timeout: 30 * time.Second,
	}

	res, err := client.Do(req)
	if err != nil {
		log.Fatalf("error making http request: %s\n", err)
	}
	defer func() {
		_ = res.Body.Close()
	}()

	// Check response from server
	if res.StatusCode != http.StatusOK {
		resBody, err := ioutil.ReadAll(res.Body)
		if err != nil {
			log.Fatalf("error reading response body: %s\n", err)
		}
		log.Fatalf("error response with status code %d: %s\n", res.StatusCode, resBody)
	}

	// Try to parse response as json
	var jsonResponse serverResponse
	if err := json.NewDecoder(res.Body).Decode(&jsonResponse); err != nil {
		log.Fatal(err)
	}

	// Ask for decryption password
	fmt.Printf("(2/2) Enter password for decrypting: ")
	decKey, err := term.ReadPassword(int(syscall.Stdin))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("")

	// Try to decrypt content using password from above
	ct, err := base64.StdEncoding.DecodeString(jsonResponse.Data.EncryptedText)
	if err != nil {
		log.Fatal(err)
	}
	pt, err := Decrypt(ct, decKey)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Decrypt successful: %s\n", pt)
}

func addSecret(serverAddr string) {
	requestURL := fmt.Sprintf("http://%s/db", serverAddr)

	var secretId string
	fmt.Printf("(1/3) Enter identifier: ")
	fmt.Scan(&secretId)

	var secretText string
	fmt.Printf("(2/3) Enter text to be encrypted: ")
	fmt.Scan(&secretText)

	fmt.Printf("(3/3) Enter password for encrypting text above: ")
	encKey, err := term.ReadPassword(int(syscall.Stdin))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("")

	// Encrypt text input using password
	ct, err := Encrypt([]byte(secretText), encKey)
	if err != nil {
		log.Fatal(err)
	}

	// Convert encrypted text to base64 string
	base64ct := base64.StdEncoding.EncodeToString(ct)
	jsonData, err := json.Marshal(&Secret{Identifier: secretId, EncryptedText: base64ct})
	if err != nil {
		log.Fatal(err)
	}

	// Send content as json to the server
	req, err := http.NewRequest(http.MethodPost, requestURL, bytes.NewReader(jsonData))
	if err != nil {
		log.Fatalf("error creating http request: %s\n", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := http.Client{
		Timeout: 30 * time.Second,
	}

	res, err := client.Do(req)
	if err != nil {
		log.Fatalf("error making http request: %s\n", err)
	}

	// Check if content was sent successfully
	if res.StatusCode == http.StatusOK {
		fmt.Printf("Content encrypted and stored successfully\n")
	} else {
		resBody, err := ioutil.ReadAll(res.Body)
		if err != nil {
			log.Fatalf("error reading response body: %s\n", err)
		}
		log.Fatalf("error response with status code %d: %s\n", res.StatusCode, resBody)
	}
}

func Encrypt(data []byte, key []byte) ([]byte, error) {
	key, salt, err := DeriveKey(key, nil)
	if err != nil {
		return nil, err
	}

	blockCipher, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(blockCipher)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nonce, nonce, data, nil)

	ciphertext = append(ciphertext, salt...)

	return ciphertext, nil
}

func Decrypt(data []byte, key []byte) ([]byte, error) {
	salt, data := data[len(data)-32:], data[:len(data)-32]

	key, _, err := DeriveKey(key, salt)
	if err != nil {
		return nil, err
	}

	blockCipher, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(blockCipher)
	if err != nil {
		return nil, err
	}

	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}

func DeriveKey(password, salt []byte) ([]byte, []byte, error) {
	if salt == nil {
		salt = make([]byte, 32)
		if _, err := rand.Read(salt); err != nil {
			return nil, nil, err
		}
	}

	key, err := scrypt.Key(password, salt, 1<<15, 8, 1, 32)
	if err != nil {
		return nil, nil, err
	}

	return key, salt, nil
}
