#Usage of test-bbs:
#  -agwpe string
#    	AGWPE server host:port (default "localhost:8000")
#  -connect-timeout int
#    	connection timeout in ms (default 15000)
#  -debug
#    	enable debug logging
#  -local string
#    	local callsign, e.g. N7GET-9 (required)
#  -remote string
#    	BBS callsign, e.g. W1AW-1 (required)
#  -step-timeout int
#    	per-command timeout in ms (default 10000)
#  -sysop-secret string
#    	sysop password for CONFIG auth test (empty = no-auth)

go run . -debug -local N7GET -remote N7GET-2 -agwpe 127.0.0.1:8000
#go run . -debug -local N7GET -remote N7GET-2 -agwpe 192.168.68.167:8000
