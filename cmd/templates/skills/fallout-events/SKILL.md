---
name: fallout-events
description: Fallout RPG random event tables — encounters, loot, weather, quests, and narrative hooks.
tags: [game, fallout, events]
---
# Fallout RPG — Random Events System

## Using the Game Engine

Generate events via:
```
exec: python3 scripts/fallout_game.py event [category]
exec: python3 scripts/fallout_game.py loot [tier] [count]
exec: python3 scripts/fallout_game.py npc-gen [count]
exec: python3 scripts/fallout_game.py weather [set]
```

Categories: `wasteland`, `urban`, `vault`, `interior`, `special`, `atmospheric`, `quest`
Loot tiers: `junk`, `common`, `uncommon`, `rare`, `unique`

---

## When to Trigger Random Events

| Situation | Trigger |
|-----------|---------|
| Traveling between locations | Every 2 turns on the road |
| Exploring a new building | On entry |
| Resting/camping | Once during rest |
| Players are indecisive | Break the deadlock with an event |
| Narrative lull | Every 3 turns without significant action |

---

## Encounter Resolution Guide

### Combat Encounters

When a combat encounter is generated:
1. Describe the enemy appearance (1-2 sentences)
2. Give players a chance to react (fight, flee, negotiate)
3. If fighting: `enemy-add` each enemy → `initiative` → turn-based combat
4. Player turns: `check` → `damage` → `enemy-hurt`
5. Enemy turns: `enemy-attack <enemy> <target>`
6. After combat: `enemy-clear` (loot auto-rolls on kill)

**Enemy Stat Blocks** (use with `enemy-add`):

| Enemy | HP | Damage | Difficulty | Drops | Attack Skill | Special |
|-------|-----|--------|-----------|-------|-------------|---------|
| Radroach | 5 | 1d6 | 1 | junk | 8 | Swarm: +1d6 when 3+ present |
| Mole Rat | 15 | 2d6 | 2 | common | 9 | Burrow ambush: +1d6 first round |
| Feral Dog | 10 | 2d6 | 1 | junk | 9 | Pack tactics: +1 difficulty per extra dog |
| Feral Ghoul | 20 | 2d6 | 2 | common | 8 | Rad immune; +10 HP in irradiated zones |
| Glowing One | 25 | 2d6 | 2 | common | 8 | Rad burst on death (+50 rads to nearby) |
| Raider (Melee) | 25 | 3d6 | 2 | common | 10 | Negotiable (Speech difficulty 2) |
| Raider (Gunner) | 25 | 3d6 | 2 | common/uncommon | 11 | +1 difficulty when in cover |
| Radscorpion | 35 | 3d6 | 3 | uncommon | 11 | Venom sting: -3 HP/turn for 3 turns |
| Super Mutant | 50 | 4d6 | 3 | uncommon/rare | 10 | Rock throw (ranged 3d6), rad resistant |
| Super Mutant Suicider | 30 | 8d6 (explosion) | 3 | uncommon | — | Charge and detonate, AoE damage |
| Deathclaw | 80 | 5d6 | 4 | rare | 13 | Combo: extra attack on hit |
| Legendary Creature | 100+ | 5d6 | 5 | rare/unique | 14 | Mutant regen: full heal once below 50% HP |
| Robot (Protectron) | 40 | 3d6 (laser) | 3 | uncommon | 12 | EMP weakness (Science check to disable) |
| Turret | 20 | 4d6 | 2 | uncommon | 12 | Stationary; hackable (Science difficulty 3) |

**Example enemy-add commands**:
```
enemy-add "Radroach" 5 1d6 8 junk "Swarm: +1d6 when 3+ present"
enemy-add "Raider 1" 25 3d6 10 common "Negotiable"
enemy-add "Super Mutant" 50 4d6 10 uncommon "Rock throw, rad resistant"
enemy-add "Deathclaw" 80 5d6 13 rare "Combo: extra attack on hit"
```

**Enemy behavior patterns**:
- **Aggressive** (Raiders/Mutants): Attack on sight, pursue fleeing players
- **Territorial** (Deathclaws/Scorpions): Attack near nest, won't chase far
- **Swarm** (Radroaches/Ghouls): Dangerous when 3+, may flee when 1 left
- **Mechanical** (Robots/Turrets): Fixed attack patterns, can be hacked or disabled

**Encounter scaling** (adjust for player count):
- 1 player: Use base HP/numbers
- 2 players: HP × 1.5 or add 1 enemy
- 3+ players: HP × 2 or add 2 enemies

### NPC Encounters

When an NPC encounter is generated:
1. Give them a name, appearance, motive (see fallout-story skill)
2. They should offer ONE of: trade, information, quest, warning
3. Determine their true disposition based on context and narrative judgment

### Exploration Encounters

When a location/item encounter is generated:
1. Describe the discovery
2. Check if there's a skill check needed (Lockpick, Science, etc.)
3. On success: generate loot
4. On failure: minor consequence or try again later

### Hazard Encounters

When a hazard is generated:
1. Describe the danger
2. Required skill check to avoid
3. Failure consequence: HP damage, radiation, lost items
4. Success: safe passage, maybe find something useful

---

## Extended Event Tables (For GM Inspiration)

### Wasteland Road Events (d20)

| Roll | Event | Type |
|------|-------|------|
| 1 | A pack of wild dogs fighting over a corpse | Atmosphere |
| 2 | Gunfire and explosions echo in the distance | Clue |
| 3 | A crude handmade road sign pointing to "Safe Zone" | Exploration |
| 4 | An overturned Nuka-Cola delivery truck, cargo scattered | Scavenge |
| 5 | Two Raider gangs in a firefight with each other | Combat/Observe |
| 6 | A Vertibird (Brotherhood airship) streaks across the sky | Plot hook |
| 7 | An abandoned baby stroller on the road... with a landmine inside | Trap |
| 8 | A survivor's holotape recording some kind of secret | Clue |
| 9 | The ground suddenly caves in, revealing an underground passage | Exploration |
| 10 | Mutated vines burst from the ground, blocking the road | Obstacle |
| 11 | A radio still playing pre-war music among the ruins | Atmosphere |
| 12 | A Nuka-Cola vending machine flickering in the rubble | Scavenge |
| 13 | A mutant lizard stares at you, then scurries away | Atmosphere |
| 14 | A crude roadside altar built around a warhead | Lore |
| 15 | An amnesiac synth who doesn't know what they are | NPC |
| 16 | A storm is rolling in — need to find shelter | Weather |
| 17 | A pre-war bunker ventilation shaft poking out of the ground | Exploration |
| 18 | A swarm of glowing fireflies dancing in the night | Atmosphere |
| 19 | A gas station converted into a makeshift fortress | Location |
| 20 | A mysterious merchant selling bizarre wares | NPC |

### Urban Scavenging Discoveries (d12)

| Roll | Discovery |
|------|-----------|
| 1 | Unopened safe (Lockpick check) |
| 2 | Pre-war medicine cabinet (Medicine check for meds) |
| 3 | Computer terminal (Science check for intel) |
| 4 | Weapon workbench (Repair check to upgrade a weapon) |
| 5 | Hidden basement entrance |
| 6 | A set of holotapes recording the last pre-war days |
| 7 | Vending machine with a few Nuka-Colas still inside |
| 8 | Remains of a survivor's camp (evacuated) |
| 9 | Buried military supply crate |
| 10 | Graffiti wall marking nearby danger zones |
| 11 | Abandoned Power Armor frame |
| 12 | Pre-war government files (valuable intelligence) |

### Campfire Stories / Radio Rumors (d10)

Use when NPCs share information at campfire or radio broadcasts hint at quests:

| Roll | Rumor |
|------|-------|
| 1 | "They say there's an untouched vault somewhere deep in the Glowing Sea..." |
| 2 | "The Brotherhood's been scavenging something big — way more active than usual." |
| 3 | "That missing caravan? I heard the Institute's involved." |
| 4 | "Someone spotted a talking Deathclaw. Yeah, you heard me right." |
| 5 | "There's a secret lab under the Old Riverbank Inn — pre-war, been there the whole time." |
| 6 | "Some guy calling himself a prophet's been wandering the wastes, predicting a great calamity." |
| 7 | "The Raider Alliance is picking a new boss. Word is there's gonna be a bloodbath." |
| 8 | "Somebody says the nuclear plant can be restarted. If they fix it... power for the whole wasteland." |
| 9 | "The Railroad's looking for someone called 'The Courier.' Big bounty." |
| 10 | "I once saw something in the deep tunnels of the subway... no, you wouldn't believe me." |

### Environmental Flavor (d8)

Atmospheric details to sprinkle into narration:

| Roll | Detail |
|------|--------|
| 1 | The wind carries grit and the taste of rusted iron |
| 2 | Something burns in the distance, a column of smoke rising straight up |
| 3 | The Geiger counter ticks once, twice, then falls silent |
| 4 | A mutant crow perches on a power line, tilting its head to watch you |
| 5 | Cracked asphalt stretches ahead, weeds pushing through the fractures |
| 6 | A sickly-sweet chemical smell hangs in the air |
| 7 | Metal clanking echoes from the ruins — probably just the wind |
| 8 | The sunset paints the wasteland burnt orange — almost beautiful enough to forget it's the end of the world |

---

## Loot Generation Guidelines

| Context | Tier | Count |
|---------|------|-------|
| Scavenging junk piles | junk | 2-3 |
| Defeating low-tier enemies | common | 1-2 |
| Exploring abandoned buildings | common + uncommon | 2-3 |
| Defeating mid-tier enemies | uncommon | 1-2 |
| Quest completion rewards | uncommon + rare | 2-3 |
| Discovering hidden areas | rare | 1-2 |
| Boss loot | rare + unique | 1-2 |

Generate via:
```
exec: python3 scripts/fallout_game.py loot common 3
exec: python3 scripts/fallout_game.py loot rare 1
```

---

## NPC Quick Generation

Use the game engine to generate random NPCs with name, appearance, motive, knowledge, and speech style:
```
exec: python3 scripts/fallout_game.py npc-gen
exec: python3 scripts/fallout_game.py npc-gen 3
```

Each NPC includes: name, appearance (build + feature + clothing), motive, knowledge (one useful piece of information), and speech style.

---

## Weather Generation

Generate random weather with weighted probabilities:
```
exec: python3 scripts/fallout_game.py weather set
```

Weather types include: Clear, Cloudy, Overcast, Light Rain, Heavy Rain, Dusty, Dust Storm, Dense Fog, Rad Storm, Scorching Heat, Bitter Cold, Light Breeze. Each has gameplay effects.

---

## Special Encounters (Easter Eggs)

Use `event special` sparingly for rare, memorable encounters:
```
exec: python3 scripts/fallout_game.py event special
```

These include references to other games, surreal experiences, and unique rewards. Use at most once per chapter.
