module github.com/erlidev/eika

go 1.26

require (
	github.com/openai/openai-go/v3 v3.61.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/BurntSushi/toml v1.4.1-0.20240526193622-a339e1f7089c // indirect
	github.com/tidwall/gjson v1.19.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678 // indirect
	golang.org/x/mod v0.31.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/telemetry v0.0.0-20251203150158-8fff8a5912fc // indirect
	golang.org/x/tools v0.40.1-0.20260108161641-ca281cf95054 // indirect
	honnef.co/go/tools v0.7.0 // indirect
)

tool (
	golang.org/x/tools/cmd/goimports
	honnef.co/go/tools/cmd/staticcheck
)
