#error: -server is required
#Usage of ./monitor:
#  -hex
#    	also print hex dump of the decoded AX.25 frame bytes
#  -info
#    	print an ASCII-ish preview of the AX.25 info field (if present) (default true)
#  -max-info int
#    	max bytes of info preview to print (default 120)
#  -q int
#    	frame print queue size (drops if overwhelmed) (default 256)
#  -server string
#    	KISS TCP address host:port (required), e.g. 127.0.0.1:8100

go run main.go -server 192.168.68.11:8100 2>&1 | cut -f3- -d' '
