# Usage of test-bridge:
#   -left-agwpe string
#       AGWPE listen address on BBS-side bridge (default "127.0.0.1:18100")
#   -right-agwpe string
#       AGWPE listen address on client-side bridge (default "127.0.0.1:18101")
#   -connect-timeout int
#       connect timeout in ms (default 15000)
#   -debug
#       enable debug logging
#   -local string
#       test client callsign (default "N7GET-9")
#   -remote string
#       BBS callsign (default "N7GET-2")
#   -step-timeout int
#       step timeout in ms (default 10000)
#   -with-digi
#       enable ConnectVia path using fixed digi RELAY

# No digi path fields (default)
go run main.go -debug -local N7GET-9 -remote N7GET-2

# Include RELAY in AX.25 digipeater path fields
go run main.go -debug -local N7GET-9 -remote N7GET-2 -with-digi
