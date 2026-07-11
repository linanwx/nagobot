---
name: manage-skills
description: Search, install, remove, and update skills from ClawHub, a hub of 13,000+ community skills. Use when the user explicitly asks to install, remove, or update a skill, or names a capability nagobot lacks and wants it added — for example a file format it cannot read yet. Also use when a task needs a skill that is not in the list above and the hub should be searched for one.
---
# Manage Skills

Skills are downloaded from ClawHub (https://clawhub.ai), the public skill registry with 13,000+ community-contributed skills.

## Search

```
exec: {{WORKSPACE}}/bin/nagobot skill search <query>
```

## Install

```
exec: {{WORKSPACE}}/bin/nagobot skill install <slug>
```

Force re-install (overwrite existing):
```
exec: {{WORKSPACE}}/bin/nagobot skill install <slug> --force
```

## Remove

```
exec: {{WORKSPACE}}/bin/nagobot skill remove <name>
```

## Update

Update a specific skill:
```
exec: {{WORKSPACE}}/bin/nagobot skill update <name>
```

Update all hub-installed skills:
```
exec: {{WORKSPACE}}/bin/nagobot skill update
```

## List Installed

```
exec: {{WORKSPACE}}/bin/nagobot skill list
```

## Hub Configuration

Show current hub:
```
exec: {{WORKSPACE}}/bin/nagobot hub
```

Change hub URL:
```
exec: {{WORKSPACE}}/bin/nagobot hub set <url>
```

Reset to default (ClawHub):
```
exec: {{WORKSPACE}}/bin/nagobot hub reset
```

## Notes

- Installed skills are immediately available (hot-reload, no restart needed).
- After installing, use `use_skill` to load the skill's full instructions.
- Local/bundled skills are NOT overwritten unless `--force` is used.
- Both `SKILL.md` and `SKILLS.md` (OpenClaw format) are supported.
