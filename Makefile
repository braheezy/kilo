PHONY: run count

.DEFAULT_GOAL := run

kilo: main.go
	@go build -o kilo main.go

run: kilo
	@$(PWD)/kilo

count:
	@cloc --quiet main.go