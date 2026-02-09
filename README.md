Design Overview

Server Data Structure Design:
The server contains two main data structures to keep track of the state. One data structure is a map from each client's net.Conn object to a struct containing important information about each client, most critically the listener's UDP address, so that the server can direct bytes to the correct address, but also information such as the station the user is currently listening to. The other data structure stored by the server is a list of station stucts, which represents all the servers that are currently streaming. Each station structs contains a list of client structs representing the clients that are currently listening to that station. Each station struct also contains a mutex, since when determining which clients to stream to, each station iterates must read through the list of active listeners, and thus it is important that only one process is able to edit this list at the same time to prevent race conditions.

Server Thread Structure:
The server executes multiple processes at once, and thus creates many threads to meet all specifications. During setup, before any clients can connect, the server creates a new thread for each station. The goal for each thread is to continuously read from its assigned file, using the time.Ticker package to regulate the rate at which a fixed 1500 size buffer is filled, so that each thread reads at a constant speed of 16 KiB/s. Each channel thread then reads from its list of active clients (after locking the mutex), and sends the bytes to each active client's UDP port. Each channel thread is also responsible for sending the Announce message to all active clients and loop to the begining of the file when reaching an EOF marker. 

In addition to a thread running for each channel, the server runs two other major threads. The first of these threads is responsible for processing user commands from the server CLI, such as printing all channels and users, and shutting down the server. The other main thread keeps a TCP port open to accept new client_control connections. When a new client connects, this thread spawns another thread, so that each client connected to the server has its own thread. These client threads are responsible for listening to messages from their connected client, and sending the appropriate response in return.

Server Concurrency and Managing States:
To synchronize writing to the server data structures, several mutexes were implemented. Each station struct in the server contains a mutex to allow only one thread to access the active lists of clients at the same time. A global mutex to protect the hashmap from client net.Conn object and their associated ClientInfo struct was also implemented. When a client disconnects from the server, a helper function deleteClient() handles updating all global data structures and closing the client connection. 

Client Controller Design:
The client controller implements no global data strucutres. Each client controller creates only two threads, one for listening to events from the server, and one for listening to user input from the controller CLI. When either of these threads terminate, either through the user entering "q\n" in the CLI or through receiving an Invalid Command message from the server, a channel it utilized to synchronize the state across the client controller and allow the main thread to terminate.

Client Listener Design:
The client listener listens on a UDP port and prints all bytes it receives. The listener does not implement any global data structures nor does it create any threads.
