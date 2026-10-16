//go:build windows

package supervisor

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const containerMappings = `Software\Classes\Local Settings\Software\Microsoft\Windows\CurrentVersion\AppContainer\Mappings`

const profilePrefix = "aiios."

func ourProfile(moniker string) bool { return strings.HasPrefix(moniker, profilePrefix) }

func DeleteStaleContainerProfiles() ([]string, error) { return deleteProfiles(ourProfile) }

func deleteProfiles(match func(string) bool) ([]string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, containerMappings, registry.ENUMERATE_SUB_KEYS)
	if errors.Is(err, registry.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the account's container profiles: %w", err)
	}
	sids, err := k.ReadSubKeyNames(-1)
	k.Close()
	if err != nil {
		return nil, fmt.Errorf("read the account's container profiles: %w", err)
	}
	var deleted []string
	var errs []error
	for _, sid := range sids {
		moniker, err := profileMoniker(sid)
		if err != nil || !match(moniker) {
			continue
		}
		if err := deleteChildProfile(moniker); err != nil {
			errs = append(errs, err)
			continue
		}
		deleted = append(deleted, moniker)
	}
	return deleted, errors.Join(errs...)
}

func profileMoniker(sid string) (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, containerMappings+`\`+sid, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	m, _, err := k.GetStringValue("Moniker")
	return m, err
}
