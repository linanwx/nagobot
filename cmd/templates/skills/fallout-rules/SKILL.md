---
name: fallout-rules
description: "Game mechanics: 2d20 skill checks, damage dice, encounter budget (tier/HP limits per chapter), AP system, radiation penalties, consumables & addiction, status effects, and leveling. Load this when you need rules for checks, combat, or item usage."
tags: [game, fallout, rules]
---
# Fallout RPG Rules Reference

## Game Engine Script

All game mechanics are handled by the script `scripts/fallout_game.py`. Use `exec` to call it:

```
exec: python3 scripts/fallout_game.py <command> [args...]
```

Run `python3 scripts/fallout_game.py help` to see all commands.

---

## S.P.E.C.I.A.L. Attributes

Each attribute ranges 1-10. Character creation: base 4 each, 12 points to distribute (total 40).

| Attribute | Abbr | Primary Effect | Secondary |
|-----------|------|---------------|-----------|
| Strength | STR | Melee damage (auto STR check), carry weight | Intimidation |
| Perception | PER | Ranged accuracy, detection | Trap awareness |
| Endurance | END | HP, poison/rad resistance | Sprint duration |
| Charisma | CHA | Speech, barter prices | Companion morale |
| Intelligence | INT | Hacking, science, repair | Skill-up bonus |
| Agility | AGI | Dodge, sneak, initiative | AP recovery |
| Luck | LCK | Luck check on every skill check | Re-roll chance |

### Derived Stats
- **HP** = END × 10
- **Carry Weight** = 150 + STR × 10
- **Initiative** = PER + AGI (for combat turn order)

### Luck Check (Automatic)
Every skill check automatically includes a Luck check for the leader:
- Rolls d100 vs effective LCK — triggers if d100 ≤ LCK (i.e. LCK% chance)
- If triggered: the player may choose to **re-roll the entire check**
- Multi-player checks: only the leader's LCK is used
- The GM should ask the player whether to re-roll when Luck triggers

| LCK | Trigger Rate |
|-----|-------------|
| 4 | 4% |
| 6 | 6% |
| 8 | 8% |
| 10 | 10% |

---

## Skills

10 skills, level 0-6. Pick 3 tag skills at character creation (start at level 2).

| Skill | Uses |
|-------|------|
| Lockpick | Open locks, bypass security, disable traps |
| Medicine | Heal wounds, diagnose illness, craft chems |
| Melee | Close combat, blocking, intimidation with weapon |
| Repair | Fix equipment, modify weapons, jury-rig solutions |
| Science | Hack terminals, analyze samples, craft tech |
| Small Guns | Firearms accuracy, maintenance, quick draw |
| Sneak | Stealth movement, pickpocket, ambush |
| Speech | Persuasion, deception, intimidation, negotiation |
| Survival | Tracking, foraging, navigation, endurance travel |
| Barter | Haggling, appraisal, trade negotiation |

Any attribute can pair with any skill — the GM chooses the attribute based on the player's described approach. For example, Lockpick + PER (carefully examine the lock), Lockpick + STR (force it open), or Lockpick + INT (study the mechanism).

### Tag Skill Bonus
Tag skills get a special crit: any roll ≤ skill level counts as a critical success (+1 extra success).

---

## Skill Checks (2d20 System)

```
exec: python3 scripts/fallout_game.py check <players> <attribute> <skill> <difficulty> [ap_spend]
```

### How It Works
1. Target Number = Effective Attribute + Skill Level (radiation/drug modifiers applied automatically)
2. Roll 2d20 (solo), 3d20 (assisted), or more (group)
3. Each die ≤ Target Number = 1 success
4. Die = 1 → Critical success (counts as 2 successes)
5. Die = 20 → Complication (something goes wrong, even on success)
6. Tag skill: die ≤ skill level → extra success

### Solo / Assisted / Group Checks

All use a single `check` command with comma-separated player names:
- **Solo** (1 player): `check Jake PER Lockpick 2` → rolls 2d20
- **Assisted** (2 players): `check Jake,Sarah PER Lockpick 3` → rolls 3d20
- **Group** (3+ players): `check Jake,Sarah,Bob PER Sneak 4` → rolls 4d20

The engine auto-selects the **leader** (highest target number). Each helper contributes 1d20 evaluated against their own target. Leader must score ≥1 success for helper successes to count.

### Spending AP for Extra Dice

Add `ap_spend` (0-3) as the last argument. Each AP adds 1d20. Maximum 5d20 total.

```
check Jake PER Lockpick 4 2     # Solo + 2 AP → 4d20
check Jake,Sarah PER Lockpick 3 1  # Assisted + 1 AP → 4d20
```

AP is deducted from the leader before rolling. If already at 5d20 from helpers, no AP can be spent.

### Difficulty Levels
| Difficulty | Successes | When to Use | Solo (no AP) | Solo (3 AP) |
|-----------|-----------|-------------|-------------|-------------|
| 0 | Auto | Trivial tasks | 100% | 100% |
| 1 | 1 | Simple tasks | ~75% | ~97% |
| 2 | 2 | Requires competence | ~30% | ~83% |
| 3 | 3 | Professional-level | ~5% | ~56% |
| 4 | 4 | Extremely hard | ~0.25% | ~28% |
| 5 | 5 | Near impossible | 0% | ~9% |

### Excess Successes → Action Points
Successes beyond difficulty become AP, added to the leader. AP can be spent on:
- Extra dice on checks (1 AP = 1d20)
- Extra dice on damage (1 AP = 1d6)
- Additional information from checks
- Environmental advantages

---

## Combat

### Initiative
```
exec: python3 scripts/fallout_game.py initiative
```
Players ordered by effective PER + AGI (highest first). Enemies included using their attack_skill value.

### Combat Actions (per turn)
- **Major Action** (1 per turn): Attack, use item, sprint, hack
- **Minor Action** (1 per turn): Move, aim (+1d20 next attack), draw weapon, reload
- **Free Action**: Speak, drop item

### Attack Roll
Use `check` with appropriate attribute + skill. The GM picks the attribute based on the action:
- Melee: typically STR, but AGI for nimble strikes, INT for exploiting weak points
- Ranged: typically PER, but AGI for snap shots, INT for called shots

### Damage
```
exec: python3 scripts/fallout_game.py damage <player> <weapon> [ap_spend]
```
Weapon is looked up automatically for dice count and type. Each AP spent adds 1d6 (max 3 AP).
Melee weapons auto-roll a **STR check** (2d20 vs STR, difficulty 2) — if triggered, adds STR/2 bonus damage.

### Enemy Tracking
```
exec: python3 scripts/fallout_game.py enemy-add <template>
exec: python3 scripts/fallout_game.py enemy-add <name> <template>
exec: python3 scripts/fallout_game.py enemy-add <name> <hp> <damage_dice> <attack_skill> <drops> [special]
exec: python3 scripts/fallout_game.py enemy-attack <enemy> <target_player>
exec: python3 scripts/fallout_game.py enemy-hurt <name> <amount>
```

**Enemy attacks**: Roll 1d20 vs attack_skill. Hit = roll damage dice, auto-apply to player HP. Roll 1 = critical (bonus damage). Roll 20 = fumble. Simpler than player 2d20 — enemies are threats but less nuanced.

**On kill**: `enemy-hurt` auto-rolls loot from the enemy's drops tier when an enemy reaches 0 HP. Dead enemies (HP ≤ 0) are automatically removed from the battlefield by `turn`.

### Encounter Budget

The engine enforces encounter constraints per chapter. `enemy-add` will **reject** enemies that violate these rules:

| Chapter | Max Tier | HP Budget (base) | Safe Turns |
|---------|----------|-------------------|------------|
| 1 | 1 (pests) | 30 | 2 |
| 2 | 2 (humanoids) | 60 | 2 |
| 3 | 2 | 80 | 2 |
| 4 | 3 (mutants) | 120 | 2 |
| 5 | 4 (late game) | 180 | 2 |
| 6+ | 5 (boss) | 250 | 2 |

- **Max Tier**: Highest enemy tier allowed. Tier 1 = pests (Radroach, Mole Rat), Tier 2 = humanoids (Raider, Ghoul), Tier 3 = mutants (Super Mutant, Yao Guai), Tier 4 = late game (Deathclaw, Sentry Bot), Tier 5 = boss (Legendary)
- **HP Budget**: Max total HP of all alive enemies on the battlefield. Scales with player count: `base × (1 + 0.5 × (players - 1))` → ×1.0 (1p), ×1.5 (2p), ×2.0 (3p), ×2.5 (4p), etc.
- **Safe Turns**: After chapter start, no enemies allowed at all for this many turns
- **Enemy Count Limit**: Chapter day 1: max 1 alive enemy. Day 2: max 2. Day 3+: no count limit (HP budget only). One day = 24 turns (8 time periods × 3 turns)

Damage dice (d6):
| Roll | Effect |
|------|--------|
| 1-2 | 1 damage |
| 3-4 | 2 damage |
| 5-6 | 3 damage + special effect |

Weapons (auto-looked up by `damage` command):
| Weapon | Dice | Type | Special | Ammo |
|--------|------|------|---------|------|
| Fists | 1 | Melee | Stun on effect | — |
| Knife | 2 | Melee | Bleed on effect | — |
| Pipe Wrench | 2 | Melee | Bleed on effect | — |
| Baseball Bat | 2 | Melee | Knockdown on effect | — |
| Machete | 3 | Melee | Bleed on effect | — |
| Power Fist | 3 | Melee | Stun on effect | — |
| Ripper | 3 | Melee | Bleed on effect | — |
| Super Sledge | 4 | Melee | Knockdown on effect | — |
| Pipe Pistol | 2 | Ranged | — | .38 Rounds |
| 10mm Pistol | 3 | Ranged | Pierce on effect | 10mm Ammo |
| .44 Magnum | 4 | Ranged | Knockdown on effect | .44 Ammo |
| Hunting Rifle | 4 | Ranged | Knockdown on effect | .308 Ammo |
| Combat Rifle | 4 | Ranged | — | 5.56mm Ammo |
| Combat Shotgun | 4 | Ranged | Spread (close: +1d) | Shotgun Shells |
| Laser Pistol | 3 | Ranged | Burn on effect | Fusion Cell |
| Laser Rifle | 4 | Ranged | Burn on effect | Fusion Cell |
| Plasma Rifle | 5 | Ranged | Burn on effect | Plasma Cartridge |
| Minigun | 5 | Ranged | Suppression on effect | 5mm Ammo |
| Missile Launcher | 6 | Ranged | Knockdown + AoE | Missile |
| Fat Man | 8 | Ranged | AoE + Radiation | Mini Nuke |

**Ammo**: Ranged weapons consume 1 ammo per shot (auto-deducted by `damage`). If out of ammo, the shot fails. Melee weapons have no ammo cost.

### Apply Damage
```
exec: python3 scripts/fallout_game.py hurt <player> <amount>
```

### Death & Incapacitation
- HP = 0 → Incapacitated (can't act, needs help)
- If not healed within 3 turns → Dead (or permanently injured at GM's discretion)
- Stimpak or Medicine check to stabilize

---

## Radiation

```
exec: python3 scripts/fallout_game.py rads <player> <amount>
```

| Rads | Severity | SPECIAL Penalties |
|------|----------|-------------------|
| 0-199 | None | — |
| 200-399 | Minor | END -1 |
| 400-599 | Moderate | STR -1, END -1 |
| 600-799 | Severe | STR -2, PER -1, END -2, AGI -1 |
| 800-999 | Critical | STR -3, PER -2, END -3, AGI -2 |
| 1000+ | Lethal | STR -4, PER -3, END -4, AGI -3, LCK -3 |

Penalties are automatically applied to all checks and initiative via effective SPECIAL. Effective SPECIAL values are clamped to 1-10 (radiation and drugs cannot push below 1 or above 10).

**Reduce rads**: RadAway (-100 rads), doctor visit (-200 rads)

---

## Healing

```
exec: python3 scripts/fallout_game.py heal <player> <amount>
```

| Method | HP Restored | Notes |
|--------|------------|-------|
| Stimpak | 15 | Immediate |
| Super Stimpak | 30 | Immediate |
| Medicine check (diff 1) | 10 | Needs medkit |
| Rest (safe location) | 5/hour | Must be safe |
| Food | 2-5 | Depends on food |
| Nuka-Cola | 2 | Also +1 AP |

**Medicine Bonus**: All healing (including `heal` and `use-item`) gains +2 HP per Medicine skill level. A player with Medicine 3 heals an extra 6 HP from every Stimpak or heal command.

**Survival Rest Bonus**: Rest heals 5 + Survival level HP per hour. A player with Survival 2 heals 7 HP/hour instead of 5.

---

## Trading

```
exec: python3 scripts/fallout_game.py trade <player> <base_price> buy/sell
```

CHA and Barter skill affect prices.

---

## Leveling & Progression

After completing a chapter or major quest:
```
exec: python3 scripts/fallout_game.py skill-up <player> <skill>
```

Award 1-2 skill points per major milestone. Max skill level is 6.

### INT Bonus (Automatic)
Every `skill-up` automatically includes an INT check (2d20 vs effective INT, difficulty 2). If triggered, the player gains **+1 extra skill point**. Higher INT = faster skill growth.

| INT | Trigger Rate |
|-----|-------------|
| 4 | ~10% |
| 6 | ~16% |
| 8 | ~22% |
| 10 | ~30% |

SPECIAL attributes do NOT increase through leveling (only through rare items or perks).

---

## Consumable Items

```
exec: python3 scripts/fallout_game.py use-item <player> <item_name>
```

Automatically removes item from inventory, applies effects, and checks for addiction.

| Item | Effect | Notes |
|------|--------|-------|
| Stimpak | +15 HP | |
| Super Stimpak | +30 HP | |
| RadAway | -100 Rads | |
| Rad-X | 3 turns rad resistance | |
| Nuka-Cola | +2 HP, +1 AP, +5 Rads | |
| Nuka-Cola Quantum | +10 HP, +5 AP, +10 Rads | |
| Purified Water | +5 HP | |
| Dirty Water | +2 HP, +15 Rads | |
| Buffout | 3 turns STR+3, END+3 | Addiction risk |
| Jet | 2 turns AGI+2 | Addiction risk |
| Mentats | 3 turns INT+2, PER+2 | Addiction risk |
| Psycho | 3 turns damage+3, END+1 | Addiction risk |
| Med-X | 3 turns damage reduction-2 | Addiction risk |

### Addiction
Chems with addiction risk: 15% chance (roll ≤ 3 on d20) per use. Addiction is a permanent status effect until treated by a doctor (Medicine check, difficulty 3) or Addictol.

---

## Status Effects

```
exec: python3 scripts/fallout_game.py effect <player> add <name> <duration>
exec: python3 scripts/fallout_game.py effect <player> list
exec: python3 scripts/fallout_game.py effect <player> remove <name>
```

Effects are automatically ticked down each turn via the `turn` command.
Duration -1 = permanent (addiction, mutations).

---

## Rest & Recovery

```
exec: python3 scripts/fallout_game.py rest [hours]
```

All players heal 5 HP/hour (capped at max HP). Clears all temporary status effects. Does NOT clear permanent effects (addiction).

---

## Game Modes

The game operates in one of two modes:

| Mode | Time Scale | `turn` Behavior | Auto-enter | Auto-exit |
|------|-----------|-----------------|------------|-----------|
| **Exploration** | Hours | Advance turn, cycle time of day, weather, tick effects, random events (10%) | Default; rest; last enemy killed | `enemy-add` (first enemy) |
| **Combat** | Seconds (rounds) | Advance combat round, tick effects, clear dead, auto-exit if no enemies | `enemy-add` (first enemy) | Last enemy killed; rest |

### Automatic Transitions
- `enemy-add` with 0 alive enemies → **enter combat** (output includes `mode_changed`)
- Last enemy killed via `enemy-hurt` → **exit combat** (output includes `mode_changed`)
- `rest` → **force exploration** (clears combat state)
- `set mode <exploration|combat>` → manual override

### Action Tracking
Action commands (`check`, `damage`, `use-item`, `enemy-attack`) auto-register which unit has acted this round. Output includes:
- `action_status.pending`: units that haven't acted yet
- `action_status.all_acted`: true when all living units have acted
- `action_status.hint`: advisory message to call `turn`

Advisory only — GM can call `turn` at any time, and can call action commands multiple times for the same unit.

### Combat Round vs Exploration Turn
- **Exploration turn**: increments `turn`, cycles 1 of 8 time periods (8 turns = 1 day)
- **Combat round**: increments `combat_round`, does NOT advance time

---

## Full Command Reference

```
# Game Management
init                                    # Initialize new game
status [player]                         # View full game state or player state
turn                                    # Mode-aware: exploration (time+effects+events) or combat (round+effects)
set <field> <value>                     # Set chapter/location/quest/weather/mode etc.

# Player Management
add-player <id> <name> <char> <bg> S P E C I A L skill1 skill2 skill3
remove-player <name>

# Dice & Checks
check <players> <attr> <skill> <diff> [ap]  # Skill check (comma-sep for multi-player)
roll <NdM>                              # Generic dice
damage <player> <weapon> [ap]           # Combat damage (auto weapon lookup)
initiative                              # Turn order (players + enemies)

# Enemy Tracking
enemy-add <template>                    # From template library
enemy-add <name> <template>             # Named instance from template
enemy-add <name> <hp> <dmg> <skill> <drops> [special]  # Custom
enemy-hurt <name> <amount>              # Negative heals; auto-loot on kill
enemy-attack <enemy> <target>           # 1d20 + auto-apply damage

# State Modification
hurt <player> <amount>
heal <player> <amount>
rads <player> <amount>
caps <player> <amount>
ap <player> <amount>
inventory <player> add/remove <item> [qty] # Qty defaults to 1; also supports "Item xN"
use-item <player> <item>                # Consumable (auto effects + addiction check)
effect <player> add/remove/list <effect> [turns]
rest [hours]                            # Rest & recover (heals all, clears temp effects)
skill-up <player> <skill>

# Random Generation
loot [tier] [count]                     # Random loot
trade <player> <price> buy/sell         # Price calc with CHA + Barter
npc-gen [count]                         # Random NPC

# Recovery
recover                                 # Restore from backup
```

---

## Chinese Translation Reference

**SPECIAL Attributes:**
STR 力量 · PER 感知 · END 耐力 · CHA 魅力 · INT 智力 · AGI 敏捷 · LCK 运气

**Skills:**
Small Guns 枪械 · Melee 近战 · Sneak 潜行 · Lockpick 开锁 · Science 科学 · Medicine 医疗 · Repair 修理 · Speech 口才 · Barter 交易 · Survival 生存

**Common Terms:**
HP 血量 · Rads 辐射 · Caps 瓶盖 · AP 行动点 · Tag Skill 专精技能 · Check 检定 · Critical 暴击 · Complication 失误
Stimpak 治疗针 · Super Stimpak 超级治疗针 · RadAway 消辐宁 · Rad-X 抗辐宁
Psycho 狂怒药 · Buffout 力量药 · Jet 喷射药 · Mentats 曼他特 · Nuka-Cola 核子可乐
