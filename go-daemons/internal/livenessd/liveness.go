package livenessd

import (
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"time"

	"github.com/devplayer0/lxd8s/go-daemons/internal/lxd"
	"github.com/devplayer0/lxd8s/go-daemons/internal/util"
)

func (s *Server) httpLiveness(w http.ResponseWriter, r *http.Request) {
	if err := exec.Command("pgrep", "lxd").Run(); err != nil {
		var perr *exec.ExitError
		if !errors.As(err, &perr) {
			s.logger.Printf("Failed to execute pgrep command: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	res, err := s.livenessClient.Get("http://lxd/1.0/cluster/members")
	if err != nil {
		s.logger.Printf("Failed to query LXD API: %v", err)

		members, err := lxd.GetClusterMembers()
		if err != nil {
			s.logger.Printf("Failed to get LXD members: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		if s.config.Replica < uint(len(members)/2) {
			// Special case: the cluster is initialised already and we're within the minority of members.
			// LXD will hang waiting for quorum until there's a a majority node, so let this one slip :)
			if s.config.LivenessClusterLenience != 0 && !s.livenessClusterLenienceStart.IsZero() && time.Now().Sub(s.livenessClusterLenienceStart) > s.config.LivenessClusterLenience {
				// We don't want this to go on forever, so eventually give up
				s.logger.Printf("Initial cluster members readiness has taken longer than %v, giving up", s.config.LivenessClusterLenience)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			if s.livenessClusterLenienceStart.IsZero() {
				s.livenessClusterLenienceStart = time.Now()
			}

			w.WriteHeader(http.StatusNoContent)
			return
		}

		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var data lxd.Response
	if err := util.ParseJSONBody(&data, res); err != nil {
		s.logger.Printf("Failed to parse response from LXD API: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var members []string
	if err := json.Unmarshal(data.Metadata, &members); err != nil {
		s.logger.Printf("Failed to parse members from LXD API: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Make sure cluster is initialised
	if data.StatusCode != lxd.StatusSuccess || (len(members) == 1 && members[0] == "/1.0/cluster/members/none") ||
		len(members) == 0 {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	s.livenessClusterLenienceStart = time.Unix(0, 0)

	w.WriteHeader(http.StatusNoContent)
}
