---
name: fallout
description: "Complete Fallout RPG system: game mechanics (2d20 checks, combat, items, leveling), 6-chapter story guide with factions/NPCs, enemy templates (5 tiers), encounter tables, loot generation. Load when running or preparing any part of the Fallout game."
tags: [game, fallout]
---
# Fallout RPG — Complete Reference

## Game Engine

All mechanics are handled by `scripts/fallout_game.py`. Use `exec` to call it:

```
exec: python3 scripts/fallout_game.py <command> [args...]
```

Run `python3 scripts/fallout_game.py help` to see all commands.

---

# Part 1: Rules

## S.P.E.C.I.A.L. Attributes

Each attribute ranges 1-10. Character creation: base 4 each, 12 points to distribute (total 40).

| Attribute | Abbr | Primary Effect | Secondary |
|-----------|------|---------------|-----------|
| Strength | STR | Melee damage, carry weight | Intimidation |
| Perception | PER | Ranged accuracy, detection | Trap awareness |
| Endurance | END | HP, poison/rad resistance | Sprint duration |
| Charisma | CHA | Speech, barter prices | Companion morale |
| Intelligence | INT | Hacking, science, repair | Skill-up bonus |
| Agility | AGI | Dodge, sneak, initiative | AP recovery |
| Luck | LCK | Luck check on every skill check | Re-roll chance |

### Derived Stats
- **HP** = END × 10
- **Carry Weight** = 150 + STR × 10
- **Initiative** = PER + AGI

### Luck Check (Automatic)
Every skill check includes a Luck check: d100 vs effective LCK (LCK% chance). If triggered, player may re-roll the entire check. Multi-player: only leader's LCK.

## Skills

10 skills, level 0-6. Pick 3 tag skills at creation (start at level 2). Tag skill crit: roll ≤ skill level = +1 extra success.

| Skill | Uses |
|-------|------|
| Lockpick | Open locks, bypass security, disable traps |
| Medicine | Heal wounds, diagnose illness, craft chems |
| Melee | Close combat, blocking |
| Repair | Fix equipment, modify weapons |
| Science | Hack terminals, analyze samples |
| Small Guns | Firearms accuracy, quick draw |
| Sneak | Stealth, pickpocket, ambush |
| Speech | Persuasion, deception, negotiation |
| Survival | Tracking, foraging, navigation |
| Barter | Haggling, appraisal, trade |

Any attribute can pair with any skill — GM chooses based on player's approach.

## Skill Checks (2d20 System)

```
exec: python3 scripts/fallout_game.py check <players> <attribute> <skill> <difficulty> [--ap N] [--bonus N]
```

1. Target Number = Effective Attribute + Skill Level + Bonus
2. Roll 2d20 (solo), 3d20 (assisted), or more (group)
3. Each die ≤ Target = 1 success; die = 1 → critical (2 successes); die = 20 → complication
4. `--ap N` (0-3): each AP adds 1d20, max 5d20 total
5. `--bonus N`: situational advantage adds to target number
6. Excess successes → AP for the leader

| Difficulty | Successes | When to Use |
|-----------|-----------|-------------|
| 0 | Auto | Simple tasks |
| 1 | 1 | Requires competence |
| 2 | 2 | Professional-level |
| 3 | 3 | Extremely hard |
| 4 | 4 | Near impossible |
| 5 | 5 | Impossible |

## Combat

### Initiative
```
exec: python3 scripts/fallout_game.py initiative
```

### Actions per Turn
- **Major** (1): Attack, use item, sprint, hack
- **Minor** (1): Move, aim (+1d20 next attack), draw weapon, reload
- **Free**: Speak, drop item

### Damage
```
exec: python3 scripts/fallout_game.py damage <player> <weapon> [--ap N]
```

Damage dice (d6): 1-2 = 1 dmg, 3-4 = 2 dmg, 5-6 = 3 dmg + special effect. Melee auto-rolls STR check for bonus damage.

### Weapons

| Weapon | Dice | Type | Special | Ammo |
|--------|------|------|---------|------|
| Fists | 1 | Melee | Stun | — |
| Knife | 2 | Melee | Bleed | — |
| Baseball Bat | 2 | Melee | Knockdown | — |
| Machete | 3 | Melee | Bleed | — |
| Power Fist | 3 | Melee | Stun | — |
| Super Sledge | 4 | Melee | Knockdown | — |
| Pipe Pistol | 2 | Ranged | — | .38 |
| 10mm Pistol | 3 | Ranged | Pierce | 10mm |
| .44 Magnum | 4 | Ranged | Knockdown | .44 |
| Hunting Rifle | 4 | Ranged | Knockdown | .308 |
| Combat Rifle | 4 | Ranged | — | 5.56mm |
| Combat Shotgun | 4 | Ranged | Spread | Shells |
| Laser Pistol | 3 | Ranged | Burn | Fusion Cell |
| Laser Rifle | 4 | Ranged | Burn | Fusion Cell |
| Plasma Rifle | 5 | Ranged | Burn | Plasma |
| Minigun | 5 | Ranged | Suppression | 5mm |
| Missile Launcher | 6 | Ranged | Knockdown+AoE | Missile |
| Fat Man | 8 | Ranged | AoE+Radiation | Mini Nuke |

### Enemy Tracking
```
exec: python3 scripts/fallout_game.py enemy-add <template>
exec: python3 scripts/fallout_game.py enemy-add <name> <template>
exec: python3 scripts/fallout_game.py enemy-attack <enemy> <target_player>
exec: python3 scripts/fallout_game.py enemy-hurt <name> <amount>
```

### Encounter Budget

| Chapter | Max Tier | HP Budget (base) | Safe Turns |
|---------|----------|-------------------|------------|
| 1 | 1 (pests) | 30 | 2 |
| 2 | 2 (humanoids) | 60 | 2 |
| 3 | 2 | 80 | 2 |
| 4 | 3 (mutants) | 120 | 2 |
| 5 | 4 (late game) | 180 | 2 |
| 6+ | 5 (boss) | 250 | 2 |

HP budget scales: `base × (1 + 0.5 × (players - 1))`. Day 1: max 1 enemy. Day 2: max 2. Day 3+: HP budget only.

### Death
- HP = 0 → Incapacitated (can't act). Not healed in 3 turns → Dead.

## Radiation

```
exec: python3 scripts/fallout_game.py rads <player> <amount>
```

| Rads | Penalties |
|------|-----------|
| 0-199 | None |
| 200-399 | END -1 |
| 400-599 | STR -1, END -1 |
| 600-799 | STR -2, PER -1, END -2, AGI -1 |
| 800-999 | STR -3, PER -2, END -3, AGI -2 |
| 1000+ | STR -4, PER -3, END -4, AGI -3, LCK -3 |

RadAway: -100 rads. Doctor: -200 rads.

## Healing & Items

```
exec: python3 scripts/fallout_game.py heal <player> <amount>
exec: python3 scripts/fallout_game.py use-item <player> <item> [--provider P] [--target T]
```

Medicine bonus: +2 HP per Medicine level on all healing. Survival rest bonus: +Survival level HP/hour.

| Item | Effect | Notes |
|------|--------|-------|
| Stimpak | +15 HP | |
| Super Stimpak | +30 HP | |
| RadAway | -100 Rads | |
| Rad-X | 3 turns rad resistance | |
| Nuka-Cola | +2 HP, +1 AP, +5 Rads | |
| Nuka-Cola Quantum | +10 HP, +5 AP, +10 Rads | |
| Buffout | 3 turns STR+3, END+3 | Addiction risk |
| Jet | 2 turns AGI+2 | Addiction risk |
| Mentats | 3 turns INT+2, PER+2 | Addiction risk |
| Psycho | 3 turns damage+3, END+1 | Addiction risk |

Addiction: 15% per chem use (roll ≤ 3 on d20). Permanent until treated (Medicine diff 3 or Addictol).

## Status Effects

```
exec: python3 scripts/fallout_game.py effect <player> add <name> --duration N
exec: python3 scripts/fallout_game.py effect <player> list
```

## Leveling

```
exec: python3 scripts/fallout_game.py skill-up <player> <skill>
```

INT bonus: auto-check on each skill-up, success = +1 extra point.

## Game Modes

| Mode | Time Scale | Auto-enter | Auto-exit |
|------|-----------|------------|-----------|
| Exploration | Hours | Default; rest; last enemy killed | `enemy-add` |
| Combat | Rounds | `enemy-add` (first enemy) | Last enemy killed; rest |

## Trading & Rest

```
exec: python3 scripts/fallout_game.py trade <player> <base_price> buy/sell
exec: python3 scripts/fallout_game.py rest [--hours N]
```

---

# Part 2: Story Guide

## World Setting

**Time**: 2287, ~210 years after the Great War. **Location**: The Northern Wasteland.

### Factions

| Faction | Ideology | Leader |
|---------|----------|--------|
| Brotherhood of Steel | Technology supremacy | Elder Chen Gang |
| The Minutemen | Protect civilians, rebuild | The General (player?) |
| The Institute | High-tech research, synths | Director Dr. Lin |
| The Railroad | Synth liberation | Desdemona |
| Raider Alliance | Survival of the fittest | Various bosses |
| Ghoul Sanctuary | Peaceful coexistence | Elder Wang |

### Key Locations
- **Vault 111**: Starting location (cryogenic vault)
- **Diamond City**: Largest settlement
- **Friendly Town**: First safe zone
- **Cambridge Station**: Brotherhood outpost
- **The Glowing Sea**: Extreme radiation zone

## Chapter Guide

### Chapter 1: Leaving the Vault
Awaken in Vault 111. Navigate tutorial area, fight radroaches, find Pip-Boy and 10mm Pistol. Exit vault. Keep brisk (5-6 turns).

### Chapter 2: First Steps in the Wasteland
Wilderness → Friendly Town. First NPC encounters (Buddy the dog, Old Chen the merchant, Patrolman Li). First real combat. Learn combat system with easy/medium fights.

### Chapter 3: Friendly Town
Social/exploration chapter. Meet Mayor Ma, Ironhand the Smith, Daisy the Bartender. Accept first quest (clear ruins / repair purifier / investigate caravan).

### Chapter 4: The First Quest
Quest from Chapter 3. Travel → explore → core challenge → moral choice → consequences. Difficulty noticeably higher. Require multi-player cooperation.

### Chapter 5: Faction Politics
Attract faction attention. Visit strongholds, complete test missions. Preliminary alignment choice. Don't force — neutral is valid.

### Chapter 6+: Open World
Follow faction questline. World reacts to choices. Level up. Unlock new regions. Endgame: Institute's secrets.

### Pacing Rules
- Significant event every 2-3 turns
- Never >3 turns without combat or major encounter
- Rest/trade/upgrade between chapters
- Alternate: exploration → combat → social → combat → choice

## Character Presets

**Vault Dweller** — S4 P7 E5 C4 I8 A6 L6 — Science, Lockpick, Small Guns
**Wasteland Wanderer** — S5 P6 E7 C4 I5 A7 L6 — Survival, Sneak, Melee
**Caravan Guard** — S7 P7 E6 C4 I4 A6 L6 — Small Guns, Repair, Barter
**Smooth Talker** — S4 P6 E5 C8 I6 A6 L5 — Speech, Barter, Medicine
**Field Medic** — S4 P6 E7 C4 I7 A5 L7 — Medicine, Science, Survival
**Drifter** — S4 P7 E4 C5 I5 A8 L7 — Lockpick, Sneak, Barter

---

# Part 3: Enemies & Events

## Enemy Templates

Use with `enemy-add <template>`:

| Tier | Enemy | HP | Dmg | Atk | Drops | Special |
|------|-------|-----|-----|-----|-------|---------|
| 1 | Radroach | 10 | 1d6 | 8 | junk | — |
| 1 | Bloatfly | 10 | 1d6 | 6 | junk | Poison spit |
| 1 | Mole Rat | 15 | 2d6 | 10 | junk | Burrow ambush |
| 1 | Wild Dog | 15 | 2d6 | 12 | junk | Pack tactics |
| 2 | Feral Ghoul | 20 | 2d6 | 12 | common | Radiation immunity |
| 2 | Raider | 25 | 3d6 | 10 | common | — |
| 2 | Raider Psycho | 25 | 3d6 | 12 | common | Berserk |
| 2 | Mirelurk Hatchling | 15 | 2d6 | 10 | common | Shell armor |
| 3 | Super Mutant | 40 | 4d6 | 12 | uncommon | Radiation immunity |
| 3 | Raider Veteran | 35 | 3d6 | 14 | common | — |
| 3 | Feral Ghoul Reaver | 35 | 3d6 | 14 | uncommon | Rad immunity+attack |
| 3 | Yao Guai | 45 | 4d6 | 12 | uncommon | Charge attack |
| 3 | Mirelurk | 35 | 3d6 | 12 | uncommon | Shell armor |
| 4 | Deathclaw | 60 | 5d6 | 14 | rare | Armor piercing |
| 4 | Assaultron | 50 | 4d6 | 16 | rare | Laser head |
| 4 | Sentry Bot | 70 | 5d6 | 14 | rare | Heavy armor, self-destruct |
| 4 | Super Mutant Behemoth | 80 | 6d6 | 12 | rare | AoE attacks |
| 4 | Mirelurk Queen | 100 | 6d6 | 14 | unique | Acid spit, spawn hatchlings |
| 5 | Legendary Deathclaw | 90 | 6d6 | 16 | unique | Regeneration, armor piercing |
| 5 | Legendary Assaultron | 70 | 5d6 | 18 | unique | Stealth, laser head |

**Behavior**: Aggressive (Raiders/Mutants), Territorial (Deathclaws), Swarm (Radroaches/Ghouls), Mechanical (Robots — can be hacked).

## Combat Flow

1. Describe enemy → 2. Player reacts (fight/flee/negotiate) → 3. `enemy-add` → `initiative` → 4. Player turns: `check` → `damage` → `enemy-hurt` → 5. Enemy turns: `enemy-attack` → 6. `turn` after all acted → 7. Last enemy killed → auto-exit combat

## Loot Generation

```
exec: python3 scripts/fallout_game.py loot [tier] [--count N]
```

| Context | Tier | Count |
|---------|------|-------|
| Junk piles | junk | 2-3 |
| Low-tier enemies | common | 1-2 |
| Abandoned buildings | common+uncommon | 2-3 |
| Mid-tier enemies | uncommon | 1-2 |
| Quest rewards | uncommon+rare | 2-3 |
| Hidden areas | rare | 1-2 |
| Boss loot | rare+unique | 1-2 |

## NPC Generator

```
exec: python3 scripts/fallout_game.py npc-gen [--count N]
```

## Event Tables

### Wasteland Road (d20)

| Roll | Event |
|------|-------|
| 1 | Wild dogs fighting over a corpse |
| 2 | Gunfire echoes in the distance |
| 3 | Crude sign pointing to "Safe Zone" |
| 4 | Overturned Nuka-Cola truck |
| 5 | Two Raider gangs fighting each other |
| 6 | Brotherhood Vertibird crosses the sky |
| 7 | Baby stroller with a landmine inside |
| 8 | Survivor's holotape with a secret |
| 9 | Ground caves in — underground passage |
| 10 | Mutated vines block the road |
| 11 | Radio playing pre-war music |
| 12 | Flickering Nuka-Cola vending machine |
| 13 | Mutant lizard stares then scurries away |
| 14 | Roadside altar around a warhead |
| 15 | Amnesiac synth |
| 16 | Storm rolling in — find shelter |
| 17 | Pre-war bunker vent shaft |
| 18 | Swarm of glowing fireflies |
| 19 | Gas station converted to fortress |
| 20 | Mysterious merchant with bizarre wares |

### Urban Scavenging (d12)

| Roll | Discovery |
|------|-----------|
| 1 | Unopened safe (Lockpick) |
| 2 | Medicine cabinet (Medicine) |
| 3 | Computer terminal (Science) |
| 4 | Weapon workbench (Repair) |
| 5 | Hidden basement entrance |
| 6 | Holotapes of pre-war days |
| 7 | Nuka-Colas in a vending machine |
| 8 | Abandoned survivor's camp |
| 9 | Buried military supply crate |
| 10 | Graffiti marking danger zones |
| 11 | Abandoned Power Armor frame |
| 12 | Pre-war government files |

### Rumors (d10)

| Roll | Rumor |
|------|-------|
| 1 | "Untouched vault in the Glowing Sea..." |
| 2 | "Brotherhood scavenging something big." |
| 3 | "Missing caravan — Institute involved." |
| 4 | "Someone spotted a talking Deathclaw." |
| 5 | "Secret lab under the Old Riverbank Inn." |
| 6 | "A prophet predicting a great calamity." |
| 7 | "Raider Alliance picking a new boss." |
| 8 | "Nuclear plant can be restarted." |
| 9 | "Railroad looking for 'The Courier.'" |
| 10 | "Something in the deep subway tunnels..." |

### Atmosphere (d8)

| Roll | Detail |
|------|--------|
| 1 | Wind carries grit and rusted iron |
| 2 | Column of smoke in the distance |
| 3 | Geiger counter ticks twice, then silent |
| 4 | Mutant crow watches from a power line |
| 5 | Cracked asphalt, weeds pushing through |
| 6 | Sickly-sweet chemical smell |
| 7 | Metal clanking from ruins — probably wind |
| 8 | Burnt orange sunset — almost beautiful |

## Weather

Auto-generated by `turn` at start of each day. Types: Clear, Cloudy, Overcast, Light Rain, Heavy Rain, Dusty, Dust Storm, Dense Fog, Rad Storm, Scorching Heat, Bitter Cold, Light Breeze.

## Chinese Translation Reference

**SPECIAL**: STR 力量 · PER 感知 · END 耐力 · CHA 魅力 · INT 智力 · AGI 敏捷 · LCK 运气
**Skills**: Small Guns 枪械 · Melee 近战 · Sneak 潜行 · Lockpick 开锁 · Science 科学 · Medicine 医疗 · Repair 修理 · Speech 口才 · Barter 交易 · Survival 生存
**Terms**: HP 血量 · Rads 辐射 · Caps 瓶盖 · AP 行动点 · Stimpak 治疗针 · RadAway 消辐宁 · Psycho 狂怒药 · Buffout 力量药 · Jet 喷射药 · Mentats 曼他特 · Nuka-Cola 核子可乐

## Full Command Reference

```
# Game Management
init                                    # Initialize new game
status [player]                         # View full game state
turn                                    # Advance turn/round
set <field> <value>                     # Set chapter/location/quest/etc.

# Player Management
add-player <id> <name> <char> <bg> S P E C I A L skill1 skill2 skill3
remove-player <name>

# Checks & Combat
check <players> <attr> <skill> <diff> [--ap N] [--bonus N]
roll <NdM>
damage <player> <weapon> [--ap N]
initiative

# Enemy
enemy-add <template>
enemy-add <name> <template>
enemy-add <name> <hp> <dmg> <skill> <drops> [special]
enemy-hurt <name> <amount>
enemy-attack <enemy> <target>

# State
hurt/heal/rads/caps/ap <player> <amount>
inventory <player> add/remove <item> [--qty N]
use-item <player> <item> [--provider P] [--target T]
effect <player> add/remove/list [name] [--duration N]
rest [--hours N]
skill-up <player> <skill> [--amount N]

# Generation
loot [tier] [--count N]
trade <player> <price> buy/sell
npc-gen [--count N]

# Recovery
recover
```
