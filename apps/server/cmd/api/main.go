package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"ali-mdm/server/internal/apk"
	"ali-mdm/server/internal/auth"
	"ali-mdm/server/internal/httpapi"
	"ali-mdm/server/internal/store"
)

// env reads ALIMDM_<name>, falling back to the older FK_<name> so a container
// started with the previous environment keeps working across the rename.
func env(name string) string {
	if v := strings.TrimSpace(os.Getenv("ALIMDM_" + name)); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("FK_" + name))
}

func envOr(name, def string) string {
	if v := env(name); v != "" {
		return v
	}
	return def
}

// atoiOr parses n, falling back to def for empty or malformed values so a typo
// in configuration degrades to the default rather than disabling alerting.
func atoiOr(s string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && v > 0 {
		return v
	}
	return def
}

func main() {
	dbPath := envOr("DB", "alimdm.db")
	secret := envOr("SECRET", "change-me-in-prod")
	apkRoot := envOr("APK_ROOT", "./apks")
	mqttURL := env("MQTT_URL") // optional
	addr := envOr("ADDR", ":8080")
	enrollToken := envOr("ENROLL_TOKEN", "alimdm-enroll")
	baseURL := envOr("BASE_URL", "http://localhost:8080")
	consoleDir := env("CONSOLE_DIR")     // e.g. ../console/dist
	provisionAPK := env("PROVISION_APK") // path to Ali MDM APK for zero-touch QR

	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer st.Close()

	signer := auth.NewSigner(secret)
	apks := apk.NewStore(apkRoot)
	// Agent builds live beside the managed catalogue, never inside it.
	agentRoot := envOr("AGENT_APK_ROOT", filepath.Join(filepath.Dir(apkRoot), "agent"))
	agentAPKs := apk.NewStore(agentRoot)
	pokes := httpapi.NewPokeQueue()

	if mqttURL != "" {
		if err := httpapi.NewMQTTBridge(pokes).Start(mqttURL); err != nil {
			log.Printf("mqtt: %v (continuing without push)", err)
		}
	}

	// Offline alerting: a silent tablet still looks fine in the room, so the
	// only way anyone learns is if something tells them. No webhook = no watcher.
	if w := httpapi.NewAlertWatcher(st, env("ALERT_WEBHOOK_URL"), baseURL,
		atoiOr(env("ALERT_OFFLINE_MINUTES"), 15)); w != nil {
		w.Start(context.Background())
	}

	srv := httpapi.New(st, signer, apks, agentAPKs, pokes, enrollToken, baseURL, consoleDir, provisionAPK)
	if provisionAPK != "" {
		log.Printf("zero-touch provisioning APK: %s", provisionAPK)
	}
	log.Printf("Ali MDM cloud API listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, srv.Routes()))
}
