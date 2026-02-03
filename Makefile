all:
	go build ./cmd/snowcast_control
	go build ./cmd/snowcast_server
	go build ./cmd/snowcast_listener

clean:
	rm -fv snowcast_control snowcast_server snowcast_listener