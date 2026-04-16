#Usage of ./kiss_router:
#  -baud int
#    	serial baud rate (default 57600)
#  -kiss-serial string
#    	upstream KISS TNC serial device, e.g. /dev/ttyUSB0
#  -kiss-tcp string
#    	upstream KISS TNC via TCP host:port (alternative to serial)
#  -listen string
#    	TCP KISS server listen address (default ":8100")
#  -max-clients int
#    	max simultaneous TCP KISS clients (default 8)
#  -stats duration
#    	stats print interval (0 to disable) (default 30s)

go run main.go -debug -baud=57600 -kiss-serial=/dev/tty.usbmodem114201
