---
name: "Description Over Cap (Skill) Fixture"
description: "This description is deliberately padded well past the skill file class's five hundred character cap so that DESCRIPTION_OVER_CAP fires for a skill-classified document, exercising the per-class cap boundary at its middle tier and nothing else in this otherwise-conformant fixture. Skills get a larger budget than context files because their description is the functional routing and keyword-matching field an operator scans to decide whether to load the skill at all, so the cap has to be generous enough for that job while still bounding the field."
workspace:
  id: "skill:tooling:description-over-cap-skill-fixture"
  tags:
    - type:skill
    - status:complete
    - privacy:internal
    - topic:tooling
  links:
    - knowledge-base:tooling:frontmatter-fixtures
  updated: 2026-07-01T00:00:00Z
---
Fixture body.
