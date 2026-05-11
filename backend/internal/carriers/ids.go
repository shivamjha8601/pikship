package carriers

import (
	"github.com/google/uuid"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// Stable carrier IDs. There is no `carrier` table — rate cards reference a
// carrier UUID and so does the pricing API. These constants are the canonical
// IDs the seed migrations use; new carrier integrations should declare theirs
// here so the pricing engine and adapters agree.

// DelhiveryCarrierID matches the rate_card.carrier_id in migration
// 0022_seed_delhivery_rate_card.
var DelhiveryCarrierID = core.CarrierIDFromUUID(uuid.MustParse("d0d1f1e7-0000-4000-8000-000000000001"))
