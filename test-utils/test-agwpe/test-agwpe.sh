#Usage of ./test-agwpe:
#  -agwpe string
#    	AGWPE server host:port (default "localhost:8000")
#  -connect-timeout int
#    	connection timeout in ms (default 15000)
#  -debug
#    	enable debug logging
#  -heard
#    	enable heard stations test (default: skip)
#  -local string
#    	local callsign, e.g. N7GET-9 (required)
#  -login-pass string
#    	AGWPE login password
#  -login-user string
#    	AGWPE login username (empty = skip login)
#  -port int
#    	AGWPE radio port number (0 = port 1)
#  -remote string
#    	remote callsign for connection tests (optional)
#  -step-timeout int
#    	per-test timeout in ms (default 5000)

#go run . -debug -agwpe 192.168.68.11:8000 -port 0 -local N7GET -remote W7SCS-1
#go run . -debug -agwpe 192.168.68.167:8000 -port 0 -local N7GET -remote N7GET-2
go run . -debug -agwpe 127.0.0.1:8000 -port 0 -local N7GET -remote W7SCS-1
