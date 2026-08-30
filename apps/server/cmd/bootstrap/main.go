package main

import (
	"flag"
	"log"
	"os"
	"strings"
	"time"

	"ali-mdm/server/internal/auth"
	"ali-mdm/server/internal/config"
	"ali-mdm/server/internal/store"
)

func main() {
	db := flag.String("db", "alimdm.db", "sqlite path")
	email := flag.String("email", "admin@school.local", "operator email")
	name := flag.String("name", "", "operator display name")
	password := flag.String("password", "", "operator password (required)")
	groupID := flag.String("group", "default", "default group id")
	groupName := flag.String("group-name", "Default", "default group name")
	apps := flag.String("apps", "", "comma-separated package names to seed")
	flag.Parse()

	if *password == "" {
		log.Fatal("-password is required")
	}

	st, err := store.Open(*db)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer st.Close()

	// Operator
	hash, err := auth.HashPassword(*password)
	if err != nil {
		log.Fatalf("hash password: %v", err)
	}
	// The bootstrap account is the first administrator, so it can create the rest.
	if err := st.UpsertOperator(&store.Operator{
		Email:        strings.ToLower(strings.TrimSpace(*email)),
		Name:         *name,
		Role:         store.RoleAdmin,
		PasswordHash: hash,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		log.Fatalf("create operator: %v", err)
	}
	log.Printf("created administrator %s", *email)

	// Default group with lockdown config
	var managed []config.ManagedApp
	for _, a := range splitCSV(*apps) {
		managed = append(managed, config.ManagedApp{
			PackageName: a, DisplayName: a, ShowOnHomeScreen: true,
			LaunchOnBoot: true, KeepAlive: true,
		})
	}
	cfgJSON, err := config.LockdownTemplate(managed)
	if err != nil {
		log.Fatalf("build config: %v", err)
	}
	canonical, err := config.Canonical([]byte(cfgJSON))
	if err != nil {
		log.Fatalf("canonicalize: %v", err)
	}
	g := &store.Group{
		ID: *groupID, Name: *groupName,
		Config: string(canonical), ConfigHash: config.Hash(canonical),
		ConfigVersion: 1,
	}
	if err := st.UpsertGroup(g); err != nil {
		log.Fatalf("upsert group: %v", err)
	}
	log.Printf("seeded group %q with %d apps, config_hash=%s", *groupID, len(managed), g.ConfigHash)
	_ = os.Getenv
}

func splitCSV(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			if cur != "" {
				out = append(out, trimSpace(cur))
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, trimSpace(cur))
	}
	return out
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
