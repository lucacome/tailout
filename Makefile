
.PHONY: templ
templ:
	go tool templ fmt internal/views
	go tool templ generate -path internal/views

.PHONY: tailout
tailout:
	goreleaser build --snapshot --clean --single-target
