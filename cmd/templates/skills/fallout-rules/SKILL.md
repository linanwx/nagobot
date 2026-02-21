---
name: fallout-rules
description: Fallout RPG complete rules reference — SPECIAL, skills, checks, combat, radiation, healing, leveling.
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
| Strength | STR | Melee damage, carry weight | Intimidation |
| Perception | PER | Ranged accuracy, detection | Trap awareness |
| Endurance | END | HP, poison/rad resistance | Sprint duration |
| Charisma | CHA | Speech, barter prices | Companion morale |
| Intelligence | INT | Hacking, science, repair | XP bonus |
| Agility | AGI | Dodge, sneak, initiative | AP recovery |
| Luck | LCK | Crits, loot quality | Random events |

### Derived Stats
- **HP** = (END + LCK) × 5
- **Carry Weight** = 150 + STR × 10
- **Initiative** = PER + AGI (for combat turn order)

---

## Skills

10 skills, level 0-6. Pick 3 tag skills at character creation (start at level 2).

| Skill | Linked Attr | Uses |
|-------|------------|------|
| Lockpick | PER or STR or AGI | Finesse picking / Brute-force breaking / Quick bypass |
| Medicine | INT or PER | Diagnosis and treatment / Wound assessment |
| Melee | STR or AGI | Heavy strikes / Nimble close combat |
| Repair | INT or PER | Fix and modify / Inspect faults |
| Science | INT or PER | Hack terminals / Analyze samples |
| Small Guns | PER or AGI | Precision aim / Quick draw |
| Sneak | AGI or INT | Stealth movement / Ambush planning |
| Speech | CHA or INT | Charm and persuasion / Logical argument |
| Survival | END or PER | Endurance travel / Tracking and foraging |
| Barter | CHA or INT | Haggling / Appraisal and valuation |

The GM chooses the attribute based on the player's described approach. The same skill can pair with different attributes depending on how the action is performed.

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
Use `check` with appropriate skill:
- Melee → STR + Melee
- Ranged → PER + Small Guns

### Damage
```
exec: python3 scripts/fallout_game.py damage <player> <dice_count> [bonus] [ap_spend]
```
Player name required (for AP deduction). Each AP spent adds 1d6 (max 3 AP).

### Enemy Tracking
```
exec: python3 scripts/fallout_game.py enemy-add <name> <hp> <damage_dice> <attack_skill> <drops> [special]
exec: python3 scripts/fallout_game.py enemy-attack <enemy> <target_player>
exec: python3 scripts/fallout_game.py enemy-hurt <name> <amount>
exec: python3 scripts/fallout_game.py enemy-list
exec: python3 scripts/fallout_game.py enemy-clear [all]
```

**Enemy attacks**: Roll 1d20 vs attack_skill. Hit = roll damage dice, auto-apply to player HP. Roll 1 = critical (bonus damage). Roll 20 = fumble. Simpler than player 2d20 — enemies are threats but less nuanced.

**On kill**: `enemy-hurt` auto-rolls loot from the enemy's drops tier.

Damage dice (d6):
| Roll | Effect |
|------|--------|
| 1 | 1 damage |
| 2 | 2 damage |
| 3-4 | 0 damage |
| 5-6 | 1 damage + special effect |

Weapon damage dice count:
| Weapon | Dice | Special |
|--------|------|---------|
| Fists | 1 | Stun on effect |
| Knife/Pipe | 2 | Bleed on effect |
| 10mm Pistol | 3 | Pierce on effect |
| Hunting Rifle | 4 | Knockdown on effect |
| Combat Shotgun | 4 | Spread (close: +1d) |
| Laser Rifle | 4 | Burn on effect |
| Minigun | 5 | Suppression on effect |

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

Penalties are automatically applied to all checks and initiative via effective SPECIAL.

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

SPECIAL attributes do NOT increase through leveling (only through rare items or perks).

---

## Oracle (Narrative Uncertainty)

```
exec: python3 scripts/fallout_game.py oracle
```

When the GM needs to decide an uncertain narrative outcome:
| D6 | Meaning |
|----|---------|
| 1 | No, and things get worse |
| 2 | No |
| 3 | No, but there's a silver lining |
| 4 | Yes, but at a cost |
| 5 | Yes |
| 6 | Yes, and bonus benefit |

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
| Jet | 2 turns AGI+2, extra action | Addiction risk |
| Mentats | 3 turns INT+2, PER+2 | Addiction risk |
| Psycho | 3 turns damage+3, END+1 | Addiction risk |
| Med-X | 3 turns damage reduction-2 | Addiction risk |
| Buffout | 3 turns STR+3, END+3 | Addiction risk |

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
