#Usage of terminal:
#  -agwpe
#    	use AGWPE interface
#  -config string
#    	path to ax25.ini (default "ax25.ini")
#  -debug
#    	enable debug logging
#  -device string
#    	override serial device for -serial
#  -help
#    	print help
#  -info
#    	enable info logging
#  -interfaces
#    	print enabled interface modes and exit
#  -kiss
#    	use KISS TCP interface
#  -local string
#    	local callsign override
#  -port int
#    	override server port for -agwpe/-kiss
#  -serial
#    	use serial KISS interface
#  -server string
#    	override server host for -agwpe/-kiss
#
go run main.go -agwpe -server 192.168.68.162 -local n7get k7ccn-1 relay
