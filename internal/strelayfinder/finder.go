package strelayfinder

// first receive a list of relay addresses from https://relays.syncthing.net/endpoint
// get my geolocation from https://ipinfo.io/json
// find top 20 closest relay by calculating distance (like https://www.movable-type.co.uk/scripts/latlong.html)
// ping each one and find the top 5 fast response relay
// return one if asked

// bandwidth capacity is not considered
// reliability is not considered
