# For pinging VM's used for dev & testing
def ping-sweep [prefix: string, start: int, end: int] {
  let total = ($end - $start + 1)
  mut range = []; 
  for $i in $start..$end {
    $range = ($range | append $i)
  }

  let results = (
    $range | par-each { |ip|
      let host = $"($prefix)($ip)"
      print $host
      let received = (
        try {
          ping $host -c 1 -t 1
          | lines
          | where $it =~ "1 packets transmitted"
          | parse "1 packets transmitted, {received} packets received, {_} packet loss"
          | get received
          | first
          | into int
        } catch { 0 }  # default to 0 if ping fails or parse fails
      )
      { host: $host, received: $received }
    }
  )

  let success = ($results | where received > 0 | length)
  let percentage = ($success * 100 / $total)

  print $"($success)/($total) hosts up: ($percentage)%"

  print "Down hosts:"
  $results
  | where received == 0
  | get host
}
ping-sweep 192.168.10.20 1 5
