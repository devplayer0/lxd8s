package vmmd

import (
	"strconv"
	"strings"
)

func ParseDisk(s string) Disk {
	d := Disk{
		Source: s,
	}

	if strings.HasSuffix(s, ":rw") {
		d.Source = strings.TrimSuffix(s, ":rw")
	} else if strings.HasSuffix(s, ":ro") {
		d.Source = strings.TrimSuffix(s, ":ro")
		d.ReadOnly = true
	}

	return d
}

func ParseNIC(s string) NIC {
	n := NIC{
		Source: s,
	}

	fields := strings.Split(s, "/")
	if len(fields) > 1 {
		n.Source = fields[0]
		n.AllowMMDS, _ = strconv.ParseBool(fields[1])
		if len(fields) == 3 {
			n.MACAddress = fields[2]
		}
	}

	return n
}
