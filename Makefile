PHONY: run count debug

.DEFAULT_GOAL := run

kilo: main.go
	@go build -o kilo main.go

run: kilo
	@$(PWD)/kilo

count:
	@cloc --quiet main.go

debug:
	@echo "Launching delv debug session. Run the VS Code 'Connect to server' debug configuration."
	@cp -f main.go foo.txt
	@dlv debug --headless --listen 127.0.0.1:42807 -- foo.txt
