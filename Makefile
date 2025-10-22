
.PHONY: templ
templ:
	go tool templ fmt internal/views
	go tool templ generate -path internal/views
