#!/bin/bash
# Close duplicate issues - generated from oracle recommendations

set -e

echo "Closing duplicate issues..."
echo ""

# Group 1: Add compacted_at_commit field — KEEP bd-432
./bd close bd-639 --reason "Duplicate of bd-432"
./bd close bd-605 --reason "Duplicate of bd-432"
./bd close bd-555 --reason "Duplicate of bd-432"
./bd close bd-546 --reason "Duplicate of bd-432"
./bd close bd-532 --reason "Duplicate of bd-432"
./bd close bd-496 --reason "Duplicate of bd-432"

# Group 2: Add label management commands — KEEP bd-364
./bd close bd-571 --reason "Duplicate of bd-364"
./bd close bd-464 --reason "Duplicate of bd-364"

# Group 3: Add migration scripts for GitHub Issues — KEEP bd-370
./bd close bd-635 --reason "Duplicate of bd-370"
./bd close bd-529 --reason "Duplicate of bd-370"
./bd close bd-443 --reason "Duplicate of bd-370"
./bd close bd-416 --reason "Duplicate of bd-370"

# Group 4: Add performance benchmarks document — KEEP bd-376
./bd close bd-634 --reason "Duplicate of bd-376"
./bd close bd-528 --reason "Duplicate of bd-376"
./bd close bd-437 --reason "Duplicate of bd-376"
./bd close bd-410 --reason "Duplicate of bd-376"

# Group 5: Audit and document all inconsistent issues — KEEP bd-366
./bd close bd-597 --reason "Duplicate of bd-366"
./bd close bd-489 --reason "Duplicate of bd-366"
./bd close bd-424 --reason "Duplicate of bd-366"
./bd close bd-397 --reason "Duplicate of bd-366"

# Group 6: Auto-import fails in git workflows — KEEP bd-334
./bd close bd-631 --reason "Duplicate of bd-334"
./bd close bd-448 --reason "Duplicate of bd-334"

# Group 7: Code review follow-up PR #8 — KEEP bd-389
./bd close bd-633 --reason "Duplicate of bd-389"
./bd close bd-527 --reason "Duplicate of bd-389"
./bd close bd-426 --reason "Duplicate of bd-389"
./bd close bd-399 --reason "Duplicate of bd-389"

# Group 8: Code review auto-import collision detection — KEEP bd-400
./bd close bd-598 --reason "Duplicate of bd-400"
./bd close bd-490 --reason "Duplicate of bd-400"
./bd close bd-427 --reason "Duplicate of bd-400"

# Group 9: Consider batching API — KEEP bd-371
./bd close bd-651 --reason "Duplicate of bd-371"
./bd close bd-600 --reason "Duplicate of bd-371"
./bd close bd-536 --reason "Duplicate of bd-371"
./bd close bd-492 --reason "Duplicate of bd-371"
./bd close bd-429 --reason "Duplicate of bd-371"
./bd close bd-402 --reason "Duplicate of bd-371"

# Group 10: Data model status/closed_at inconsistent — KEEP bd-391
./bd close bd-594 --reason "Duplicate of bd-391"
./bd close bd-487 --reason "Duplicate of bd-391"
./bd close bd-430 --reason "Duplicate of bd-391"
./bd close bd-417 --reason "Duplicate of bd-391"

# Group 11: Document git-based restoration — KEEP bd-380
./bd close bd-638 --reason "Duplicate of bd-380"
./bd close bd-525 --reason "Duplicate of bd-380"
./bd close bd-436 --reason "Duplicate of bd-380"
./bd close bd-409 --reason "Duplicate of bd-380"

# Group 12: Epic: Add intelligent database compaction — KEEP bd-251
./bd close bd-392 --reason "Duplicate of bd-251"

# Group 13: Epic: Fix status/closed_at inconsistency — KEEP bd-367
./bd close bd-596 --reason "Duplicate of bd-367"
./bd close bd-488 --reason "Duplicate of bd-367"
./bd close bd-423 --reason "Duplicate of bd-367"
./bd close bd-396 --reason "Duplicate of bd-367"

# Group 14: GH-11 Docker support — KEEP bd-358
./bd close bd-629 --reason "Duplicate of bd-358"
./bd close bd-523 --reason "Duplicate of bd-358"

# Group 15: GH-3 Debug zsh killed error — KEEP bd-87
./bd close bd-618 --reason "Duplicate of bd-87"
./bd close bd-524 --reason "Duplicate of bd-87"
./bd close bd-510 --reason "Duplicate of bd-87"
./bd close bd-431 --reason "Duplicate of bd-87"
./bd close bd-406 --reason "Duplicate of bd-87"
./bd close bd-348 --reason "Duplicate of bd-87"

# Group 16: Git-based restoration for compacted issues — KEEP bd-404
./bd close bd-649 --reason "Duplicate of bd-404"
./bd close bd-604 --reason "Duplicate of bd-404"
./bd close bd-550 --reason "Duplicate of bd-404"
./bd close bd-495 --reason "Duplicate of bd-404"
./bd close bd-422 --reason "Duplicate of bd-404"

# Group 17: Implement bd restore command — KEEP bd-434
./bd close bd-637 --reason "Duplicate of bd-434"
./bd close bd-622 --reason "Duplicate of bd-434"
./bd close bd-607 --reason "Duplicate of bd-434"
./bd close bd-549 --reason "Duplicate of bd-434"
./bd close bd-531 --reason "Duplicate of bd-434"
./bd close bd-498 --reason "Duplicate of bd-434"

# Group 18: Improve error handling in dependency removal — KEEP bd-359
./bd close bd-650 --reason "Duplicate of bd-359"
./bd close bd-602 --reason "Duplicate of bd-359"
./bd close bd-515 --reason "Duplicate of bd-359"
./bd close bd-493 --reason "Duplicate of bd-359"

# Group 19: Low priority chore — KEEP bd-377
./bd close bd-659 --reason "Duplicate of bd-377"
./bd close bd-643 --reason "Duplicate of bd-377"
./bd close bd-547 --reason "Duplicate of bd-377"
./bd close bd-534 --reason "Duplicate of bd-377"

# Group 20: P2: Consider batching API — MERGE TO bd-371
./bd close bd-593 --reason "Duplicate of bd-371"
./bd close bd-486 --reason "Duplicate of bd-371"
./bd close bd-390 --reason "Duplicate of bd-371"

# Group 21: Reach 1.0 release milestone — KEEP bd-388
./bd close bd-632 --reason "Duplicate of bd-388"
./bd close bd-526 --reason "Duplicate of bd-388"
./bd close bd-425 --reason "Duplicate of bd-388"
./bd close bd-398 --reason "Duplicate of bd-388"

# Group 22: Record git commit hash during compaction — KEEP bd-433
./bd close bd-642 --reason "Duplicate of bd-433"
./bd close bd-641 --reason "Duplicate of bd-433"
./bd close bd-606 --reason "Duplicate of bd-433"
./bd close bd-551 --reason "Duplicate of bd-433"
./bd close bd-533 --reason "Duplicate of bd-433"
./bd close bd-497 --reason "Duplicate of bd-433"

# Group 23: Use safer placeholder pattern — KEEP bd-29
./bd close bd-603 --reason "Duplicate of bd-29"
./bd close bd-494 --reason "Duplicate of bd-29"
./bd close bd-445 --reason "Duplicate of bd-29"
./bd close bd-403 --reason "Duplicate of bd-29"

echo ""
echo "Done! Closed duplicates, kept the oldest open issue in each group."
./bd stats
