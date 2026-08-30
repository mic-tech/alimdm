package main

import (
	"strings"
	"log"
	"net/http"
	"os"

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

func main() {
	dbPath := envOr("DB", "alimdm.db")
	secret := envOr("SECRET", "change-me-in-prod")
	apkRoot := envOr("APK_ROOT", "./apks")
	mqttURL := env("MQTT_URL") // optional
	addr := envOr("ADDR", ":8080")
	enrollToken := envOr("ENROLL_TOKEN", "alimdm-enroll")
	baseURL := envOr("BASE_URL", "http://localhost:8080")
	consoleDir := env("CONSOLE_DIR") // e.g. ../console/dist
	provisionAPK := env("PROVISION_APK") // path to Ali MDM APK for zero-touch QR

	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer st.Close()

	signer := auth.NewSigner(secret)
	apks := apk.NewStore(apkRoot)
	pokes := httpapi.NewPokeQueue()

	if mqttURL != "" {
		if err := httpapi.NewMQTTBridge(pokes).Start(mqttURL); err != nil {
			log.Printf("mqtt: %v (continuing without push)", err)
		}
	}

	srv := httpapi.New(st, signer, apks, pokes, enrollToken, baseURL, consoleDir, provisionAPK)
	if provisionAPK != "" {
		log.Printf("zero-touch provisioning APK: %s", provisionAPK)
	}
	log.Printf("Ali MDM cloud API listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, srv.Routes()))
}
