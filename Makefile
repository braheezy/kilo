PHONY: run

kilo: main.go
	go build -o kilo main.go

run: kilo
	@$(PWD)/kilo
