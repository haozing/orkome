package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	Port    string `env:"PORT" envDefault:"8080"`
	AppEnv  string `env:"APP_ENV" envDefault:"dev"`
	LogLvl  string `env:"LOG_LEVEL" envDefault:"info"`
	Dsn     string `env:"DATABASE_URL,required,notEmpty"`
	Replica string `env:"DATABASE_REPLICA_URL"`

	JWTSecret string        `env:"JWT_SECRET,required,notEmpty"`
	TokenTTL  time.Duration `env:"AUTH_TOKEN_TTL" envDefault:"720h"`

	CredentialMasterKey string `env:"ENGINE_CREDENTIAL_MASTER_KEY"`

	AllowSignup    bool     `env:"ALLOW_SIGNUP" envDefault:"true"`
	AllowedEmails  []string `env:"ALLOWED_EMAILS" envSeparator:","`
	AllowedDomains []string `env:"ALLOWED_EMAIL_DOMAINS" envSeparator:","`
	DevCode        string   `env:"ORKOME_DEV_VERIFICATION_CODE"`

	RedisURL string `env:"REDIS_URL"`

	WSOrigins []string `env:"WS_ALLOWED_ORIGINS" envSeparator:","`

	TrustedProxies string `env:"ORKOME_TRUSTED_PROXIES"`

	CORSAllowedOrigins string `env:"CORS_ALLOWED_ORIGINS"`

	FeatureFlagsFile string `env:"ORKOME_FEATURE_FLAGS_FILE"`

	ChannelWSLeaseBackend string `env:"CHANNEL_WS_LEASE_BACKEND" envDefault:"sql"`

	MetricsToken string `env:"REALTIME_METRICS_TOKEN"`

	GitHubToken                string `env:"GITHUB_TOKEN"`
	SkillsShIntegrationEnabled bool   `env:"ORKOME_RUN_SKILLS_SH_INTEGRATION"`

	LocalUploadDir string `env:"LOCAL_UPLOAD_DIR" envDefault:"./uploads"`
	S3Endpoint     string `env:"S3_ENDPOINT"`
	S3Region       string `env:"S3_REGION" envDefault:"us-east-1"`
	S3AccessKey    string `env:"S3_ACCESS_KEY"`
	S3SecretKey    string `env:"S3_SECRET_KEY"`
	S3Bucket       string `env:"S3_BUCKET"`
	S3PublicBase   string `env:"S3_PUBLIC_BASE"`

	EvalSampleRate float64 `env:"ENGINE_EVAL_SAMPLE_RATE" envDefault:"0.1"`
	EvalDailyQuota int     `env:"ENGINE_EVAL_DAILY_QUOTA" envDefault:"100"`

	EgressExtraAllowCIDRs []string `env:"ENGINE_EGRESS_EXTRA_ALLOW_CIDRS" envSeparator:","`

	ResendKey  string `env:"RESEND_API_KEY"`
	ResendFrom string `env:"RESEND_FROM_EMAIL"`
	SMTPHost   string `env:"SMTP_HOST"`
	SMTPPort   int    `env:"SMTP_PORT"`
	SMTPUser   string `env:"SMTP_USERNAME"`
	SMTPPass   string `env:"SMTP_PASSWORD"`
	SMTPFrom   string `env:"SMTP_FROM_EMAIL"`
	SMTPTLS    bool   `env:"SMTP_TLS"`
}

func ParseConfig() (*Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return nil, err
	}
	if len(c.JWTSecret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must be ≥32 bytes")
	}
	if strings.Contains(c.JWTSecret, "changeme") || strings.Contains(c.JWTSecret, "secret-secret") {
		return nil, fmt.Errorf("JWT_SECRET is a known weak value, refusing to start")
	}
	if c.CredentialMasterKey != "" {
		if len(c.CredentialMasterKey) < 32 {
			return nil, fmt.Errorf("ENGINE_CREDENTIAL_MASTER_KEY must be ≥32 bytes")
		}
		if strings.Contains(c.CredentialMasterKey, "changeme") || strings.Contains(c.CredentialMasterKey, "secret-secret") {
			return nil, fmt.Errorf("ENGINE_CREDENTIAL_MASTER_KEY is a known weak value, refusing to start")
		}
	} else if c.IsProd() {
		return nil, fmt.Errorf("production must set ENGINE_CREDENTIAL_MASTER_KEY explicitly (internal archive 09 §6.2)")
	}
	if c.TokenTTL < time.Minute {
		return nil, fmt.Errorf("AUTH_TOKEN_TTL must be at least 1 minute")
	}
	if c.DevCode != "" && c.AppEnv == "production" {
		return nil, fmt.Errorf("ORKOME_DEV_VERIFICATION_CODE is only allowed in non-production")
	}
	return &c, nil
}

func (c *Config) IsProd() bool { return c.AppEnv == "production" }
