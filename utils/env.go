/*
Package utils/env handles environment bootstrapping for LikeSpotifyBot.
Injected from composition roots (app + zz-ops) before dependencies are built.
Workflow stage: process startup configuration loading.
*/
package utils

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/joho/godotenv"
)

type EnvManager struct{}

var Env = NewEnvManager()

func NewEnvManager() *EnvManager {
	return &EnvManager{}
}

// IsLive is true when APP_ENV is exactly "live" (case-insensitive). Background schedulers use this; unset/test/other values are non-live.
func (e *EnvManager) IsLive() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "live")
}

// BroadcastSchedulerEnabled is true for APP_ENV live or test (case-insensitive).
// Test uses a separate database and can run the same outbound broadcast worker (e.g. GC smoke tests).
// Other APP_ENV values skip the scheduler.
func (e *EnvManager) BroadcastSchedulerEnabled() bool {
	s := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	return s == "live" || s == "test"
}

// Init loads .env files from the current working directory (godotenv does not override vars already set by the shell/Docker).
//   - APP_ENV unset or "test" → prefer .env.test, then .env if DATABASE_URL still empty
//   - any other APP_ENV (e.g. live, production) → .env
func (e *EnvManager) Init() {
	appEnv := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	switch appEnv {
	case "", "test":
		_ = godotenv.Load(".env.test")
		if strings.TrimSpace(os.Getenv("DATABASE_URL")) == "" {
			_ = godotenv.Load(".env")
		}
	default:
		_ = godotenv.Load(".env")
	}
}

// InitOps is for zz-ops CLIs: keeps existing shell values and only backfills missing keys from env files.
// If DATABASE_URL is unset and APP_ENV is unset, prompts on an interactive terminal: "Use live .env [y/N]"
// (default N → .env.test); non-TTY defaults to .env.test. Preset APP_ENV or DATABASE_URL skips the prompt.
func (e *EnvManager) InitOps() {
	if strings.TrimSpace(os.Getenv("DATABASE_URL")) != "" {
		return
	}

	appEnv := strings.TrimSpace(os.Getenv("APP_ENV"))
	if appEnv == "" {
		if stdinIsTTY() {
			fmt.Fprintf(os.Stderr, "Use live .env [y/N]: ")
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			line = strings.TrimSpace(strings.ToLower(line))
			if line == "y" || line == "yes" {
				appEnv = "live"
			} else {
				appEnv = "test"
			}
		} else {
			appEnv = "test"
		}
		_ = os.Setenv("APP_ENV", appEnv)
	}

	switch strings.ToLower(appEnv) {
	case "", "test":
		_ = readEnvFileNoOverride(".env.test")
		if strings.TrimSpace(os.Getenv("DATABASE_URL")) == "" {
			_ = readEnvFileNoOverride(".env")
		}
	default:
		_ = readEnvFileNoOverride(".env")
	}
}

func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func readEnvFileNoOverride(path string) error {
	envs, err := godotenv.Read(path)
	if err != nil {
		return err
	}
	for k, v := range envs {
		if strings.TrimSpace(os.Getenv(k)) == "" {
			_ = os.Setenv(k, v)
		}
	}
	return nil
}

// AssetsURL joins ASSETS_URL with a relative asset path.
// If ASSETS_URL is empty, returns "".
func (e *EnvManager) AssetsURL(assetPath string) string {
	base := strings.TrimSpace(os.Getenv("ASSETS_URL"))
	if base == "" {
		return ""
	}
	base = strings.TrimRight(base, "/")
	assetPath = strings.TrimSpace(assetPath)
	if assetPath == "" {
		return base
	}
	return base + "/" + path.Clean(strings.TrimLeft(assetPath, "/"))
}
