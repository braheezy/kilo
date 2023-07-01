PHONY: run count debug

.DEFAULT_GOAL := run

kilo: main.go
	@go build -o kilo main.go

run: kilo
	@$(PWD)/kilo

count:
	@cloc --quiet main.go
	@echo "Total: $$(wc -l main.go | awk '{printf $$1}')"

debug:
	@echo "Launching delv debug session. Run the VS Code 'Connect to server' debug configuration."
	@mkdir -p debug
	@cp -f main.go debug/foo.go
	@dlv debug --headless --listen 127.0.0.1:42807 -- debug/foo.go
	@rm -r debug