Pipie - connect to your machine easily
------
[![Build Status](https://travis-ci.org/MingchenZhang/pipie.svg?branch=master)](https://travis-ci.org/MingchenZhang/pipie)

If you have an embedded computer or a device that has no public address, and want to connect to it, 
this piece of software could make your life easier.

\
Use case:
1. For example , your raspberry pi is connected to your dorm network. You would like to ssh into the device, but no public 
ip is assigned to it, and you have no control over dorm network to perform a port forward. 
2. You have a cluster of servers in a cloud, and no public ip address is assigned to those. Now you need to 
download a huge logfile from one of those machine, for local analysis. Worse, you are in a library where you are behind 
a NAT as well.  

In the above cases, you can wget the binary from github. Starts them on both end (on demand or as daemon). Pipie will 
do the hard work of finding addresses, traversing the NAT, securing the connection, or finding the best relay for your need.
All you need to do is assigning a shared username and key.   

\
Limitation: 
1. Please start server side first (one with -s flag). 
2. Safer key reading mechanism will be provided later™

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
1. UDP based NAT traversal. 
2. Relayed connection, in case NAT traversal is not possible. \
   Use syncthing relay infrastructure. Special thank to syncthing project.
3. Encrypted (no server trust is given)
4. Protocol is designed to have minimum burden on server side. \
   Don't worry, no server trust is assumed in the protocol. \
   You can also run a server you self. 
5. In alpha. Please don't panic() if you see issue. Would you kindly report it?


Server side code is simple, but too messy for public view. Will clean it up and release it later. 
