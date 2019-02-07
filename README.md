Pipie - connect to your machine easily
------
If you have an embedded computer or a device that has no public address, and want to connect to it, 
this piece of software could make your life easier.

\
Use case:
1. Your raspberry connects to your doom network, which you can't do router port forward. Make this software 
run in background, in port forward mode. You can now SSH to you rpi anywhere you want.
2. You have a cluster of servers in a cloud, and no public ip address is assigned to those. Now you need to 
download a huge logfile from one of those machine, for local analysis. Just download the binary from github and pipe your file to your local
machine.  

Notes: Please start server side first (one with -s flag). 
Safer key reading will be provided later™

\
Pipie can do the following (at least for now):
1. Establising pipe\
   Just like normal pipe, but across network.
   
   Example on both sides:\
   `./pipie --mode pipe --name my-node1 --k abcdef < file_to_send`\
   `./pipie --mode pipe -s --name my-node1 --k abcdef > send_to`
   
2. TCP port forwarding\
   Just like SSH, but no need to know the network topology and open ports
   
   Example on both sides (forward port forward):\
      `./pipie --mode port_forward -a 0.0.0.0:1234:127.0.0.1:1234 --name my-node1 --k abcdef`\
      `./pipie --mode port_forward -s --name my-node1 --k abcdef > send_to`
   
   Example on both sides (reverse port forward):\
      `./pipie --mode port_forward -r -a 0.0.0.0:1234:127.0.0.1:1234 --name my-node1 --k abcdef`\
      `./pipie --mode port_forward -s -r --name my-node1 --k abcdef > send_to`

\
Features:
1. UDP based NAT traversal
2. Relayed connection, in case NAT traversal is not possible\
   Use syncthing relay infrastructure. Special thank to syncthing project.
3. Encrypted\
   (propably don't need to be mentioned, it would be strange if it is not encrypted)\
   (also, no server trust is given)
4. In alpha. Please don't panic() if you see issues. Would you kindly report it?


Server side code is simple, but too messy for public view. Will clean it up and release it later. 
