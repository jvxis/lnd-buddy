package main

import (
	"context"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"log"
	"path/filepath"
	"time"

	"github.com/boltdb/bolt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"

	lnrpc "lndbuddy/lnrpc"

)

const (
	defaultLndDir       = "/home/admin/.lnd"
	tlsCertRelativePath = "tls.cert"
	macaroonRelativePath = "data/chain/bitcoin/mainnet/admin.macaroon"
	dbFile              = "lndbuddy.db"
	dbBucket            = "auth"
)

type LndClient struct {
	conn    *grpc.ClientConn
	client  lnrpc.LightningClient
	ctx     context.Context
	cancel  context.CancelFunc
}

func getCreds(lndDir string) (credentials.TransportCredentials, error) {
	certPath := filepath.Join(lndDir, tlsCertRelativePath)
	cert, err := ioutil.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read tls.cert: %v", err)
	}
	cp := x509.NewCertPool()
	if !cp.AppendCertsFromPEM(cert) {
		return nil, fmt.Errorf("credentials: failed to append certificates")
	}
	return credentials.NewClientTLSFromCert(cp, ""), nil
}

func getMacaroonHex(lndDir string) (string, error) {
	macaroonPath := filepath.Join(lndDir, macaroonRelativePath)
	macBytes, err := ioutil.ReadFile(macaroonPath)
	if err != nil {
		return "", fmt.Errorf("failed to read macaroon: %v", err)
	}
	return hex.EncodeToString(macBytes), nil
}

func newLndClient(lndDir string, address string) (*LndClient, error) {
	creds, err := getCreds(lndDir)
	if err != nil {
		return nil, err
	}
	macHex, err := getMacaroonHex(lndDir)
	if err != nil {
		return nil, err
	}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(creds)}
	conn, err := grpc.Dial(address, opts...)
	if err != nil {
		return nil, fmt.Errorf("could not connect to lnd: %v", err)
	}
	ctx := metadata.AppendToOutgoingContext(context.Background(), "macaroon", macHex)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	return &LndClient{conn: conn, client: lnrpc.NewLightningClient(conn), ctx: ctx, cancel: cancel}, nil
}

func (c *LndClient) GetInfo() (*lnrpc.GetInfoResponse, error) {
	defer c.cancel()
	return c.client.GetInfo(c.ctx, &lnrpc.GetInfoRequest{})
}

func saveLndPathToDB(path string) error {
	db, err := bolt.Open(dbFile, 0600, nil)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(dbBucket))
		if err != nil {
			return err
		}
		return b.Put([]byte("lnd_path"), []byte(path))
	})
}

func getSavedLndPath() (string, error) {
	db, err := bolt.Open(dbFile, 0600, nil)
	if err != nil {
		return "", err
	}
	defer db.Close()
	var path string
	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(dbBucket))
		if b == nil {
			return fmt.Errorf("auth bucket not found")
		}
		pathBytes := b.Get([]byte("lnd_path"))
		if pathBytes == nil {
			return fmt.Errorf("lnd_path not set")
		}
		path = string(pathBytes)
		return nil
	})
	return path, err
}

func main() {
	// try load config
	lndPath, err := getSavedLndPath()
	if err != nil {
		fmt.Println("Informe o caminho base do LND (ex: /home/admin/.lnd):")
		fmt.Scanln(&lndPath)
		saveLndPathToDB(lndPath)
	}

	client, err := newLndClient(lndPath, "localhost:10009")
	if err != nil {
		log.Fatalf("Erro conectando ao LND: %v", err)
	}

	info, err := client.GetInfo()
	if err != nil {
		log.Fatalf("Erro ao obter info do node: %v", err)
	}

	fmt.Printf("Node Alias: %s\n", info.Alias)
	fmt.Printf("PubKey: %s\n", info.IdentityPubkey)
	fmt.Printf("Bitcoin Sync: %v\n", info.SyncedToChain)
	fmt.Printf("Lightning Sync: %v\n", info.SyncedToGraph)
	fmt.Printf("Versão LND: %s\n", info.Version)
}
