package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"log/slog"
	"os"

	"github.com/peiblow/eeapi/internal/api"
	"github.com/peiblow/eeapi/internal/config"
	"github.com/peiblow/eeapi/internal/database/postgres"
	"github.com/peiblow/eeapi/internal/keys"
	"github.com/peiblow/eeapi/internal/swp"
)

func main() {
	vvmHost := os.Getenv("VVM_HOST")
	if vvmHost == "" {
		vvmHost = "localhost"
	}

	vvmPort := os.Getenv("VVM_PORT")
	if vvmPort == "" {
		vvmPort = "8332"
	}

	svm := swp.NewSwpClient(vvmHost + ":" + vvmPort)
	defer svm.Close()

	if err := svm.Connect(); err != nil {
		slog.Error("Failed to connect to SVM server", "error", err)
		os.Exit(1)
	}

	slog.Info("-> Connected to SVM server!")

	db, err := postgres.Open()
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	slog.Info("-> Connected to database!")

	cfg := config.Config{
		Addr: ":8080",
		DB:   config.DBConfig{},
	}

	pub, priv, err := keys.LoadOrCreateKeys("keysStore/keys.pem")
	if err != nil {
		slog.Error("Failed to load or create keys", "error", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	locker := config.NewContractLocker()

	pubKeyHex := os.Getenv("SYNX_PUBLIC_KEY")
	pubKeyBytes, _ := hex.DecodeString(pubKeyHex)
	clientPubKey := ed25519.PublicKey(pubKeyBytes[len(pubKeyBytes)-32:])

	server := api.NewServer(cfg, svm, db, pub, priv, clientPubKey, locker)

	if err := server.Run(); err != nil {
		slog.Error("Server failed to start", "error", err)
		os.Exit(1)
	}
}
