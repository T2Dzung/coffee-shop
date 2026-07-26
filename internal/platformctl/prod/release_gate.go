package prod

import (
	"strings"

	"github.com/thangchung/go-coffeeshop/internal/platformctl/component"
)

func promotedGuardArtifact(appSource, guardSource, catalogSource string) (string, string, bool) {
	zero := "sha256:" + strings.Repeat("0", 64)
	appDigests := digestPattern.FindAllString(appSource, -1)
	if len(appDigests) != 7 {
		return "", "", false
	}
	for _, digest := range appDigests {
		if digest == zero {
			return "", "", false
		}
	}
	guardDigests := digestPattern.FindAllString(guardSource, -1)
	if len(guardDigests) != 1 || guardDigests[0] == zero {
		return "", "", false
	}
	catalog, err := component.Decode([]byte(catalogSource))
	if err != nil {
		return "", "", false
	}
	guard, err := catalog.Find("platform-ownership-guard")
	if err != nil {
		return "", "", false
	}
	return guard.ImageRepository, guardDigests[0], true
}
