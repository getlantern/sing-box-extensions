/*
Package algeneva provides a sing-box compatible implementation of the Application Layer Geneva HTTP
protocol. The protocol works by applying transformations to the HTTP request to bypass censorship,
and for this reason, the request must be sent in plaintext. However, algeneva will encrypt the
connection after the connection is established with the proxy server using the provided TLS config.
In order to look like normal traffic, algeneva will upgrade the connection to a secure WebSocket
connection.

More information about the protocol can be found at github.com/getlantern/algeneva and
https://www.usenix.org/system/files/sec22-harrity.pdf
*/
package algeneva
