---
name: fallout-story
description: "6-chapter story guide (Vault → Wasteland → Town → Quest → Factions → Open World), faction details, key NPCs, pacing rules, and chapter progression. Load this when starting a new chapter or introducing factions/NPCs."
tags: [game, fallout, story]
---
# Fallout RPG — Story Guide

## World Setting

**Time**: 2287, approximately 210 years after the Great War
**Location**: The Northern Wasteland (a regional counterpart of the Capital Wasteland)
**Background**: After the Great War of 2077, nuclear bombs destroyed most of civilization. Vault-Tec built numerous underground vaults before the war. Your adventure begins in Vault 111.

### Major Factions

| Faction | Ideology | Disposition | Leader |
|---------|----------|-------------|--------|
| Brotherhood of Steel | Technology supremacy, monopolize pre-war tech | Militaristic, wary of outsiders | Elder Chen Gang |
| The Minutemen | Protect civilians, rebuild communities | Open and friendly, but weak | The General (player may assume role) |
| The Institute | High-tech research, synth manufacturing | Secretive, operates underground | Director Dr. Lin |
| The Railroad | Synth liberation organization | Underground resistance, coded contacts | Desdemona |
| Raider Alliance | Survival of the fittest | Hostile, highly territorial | Various gang bosses |
| Ghoul Sanctuary | Peaceful coexistence | Friendly but discriminated against | Elder Wang |

### Key Locations

- **Vault 111**: Starting location, cryogenic experiment vault
- **Diamond City**: Largest settlement in the wasteland, built inside a stadium
- **Friendly Town**: Small settlement, the first safe zone players reach
- **Cambridge Station**: Brotherhood of Steel forward outpost
- **The Institute**: Underground secret research facility
- **The Glowing Sea**: Ground zero of a nuclear blast, extreme radiation zone

---

## Chapter Guide

### Chapter 1: Leaving the Vault

**Opening Scenario**:
Players awaken inside cryogenic pods in Vault 111. A system malfunction triggered the thaw. Parts of the vault are damaged, and radroaches roam the corridors.

**Key Events**:
1. Awaken from cryogenic sleep; discover other pod occupants did not survive
2. Navigate the vault's tutorial area (learn basic controls, combat, exploration)
3. Find the Overseer's office; discover records of the vault's secret experiment
4. Fight through damaged sections against radroaches
5. Obtain a 10mm Pistol and a Pip-Boy (wrist computer)
6. Step through the vault door and see the wasteland for the first time

**GM Notes**:
- Keep the pace brisk — finish the vault content in 2-3 turns
- Use environmental storytelling to contrast pre-war life with post-war ruin
- Give every player a moment to shine (one picks a lock, one fights, one searches, etc.)
- Hint at the vault's experimental nature (cryo = controlled variable)

**Ending Trigger**: All players exit the vault door

---

### Chapter 2: First Steps in the Wasteland

**Setting**: The wilderness outside the vault → Friendly Town

**Key Events**:
1. First glimpse of the wasteland's desolation
2. Encounter the first NPC (could be a stray dog, a wounded traveler, or a merchant)
3. First real combat (radroach swarm or mole rats)
4. Discover a road sign or radio signal pointing toward Friendly Town
5. Arrive at Friendly Town

**NPCs**:
- **Buddy**: A loyal wasteland dog that can become a companion
- **Old Chen**: A wounded merchant — if rescued, he'll be grateful and offer discounts
- **Patrolman Li**: A Minutemen patrol soldier who gives directions and explains wasteland basics

**Random Encounters**: The `turn` command has a 10% chance to auto-generate random events. Use them to populate this chapter's travel scenes.

**GM Notes**:
- This chapter is for players to learn the combat system
- Arrange one easy fight (difficulty 1) and one medium fight (difficulty 2)
- Let players experience the wasteland's danger without being too lethal
- Encourage teamwork

**Ending Trigger**: Arrive at Friendly Town

---

### Chapter 3: Friendly Town

**Setting**: Friendly Town — a small walled settlement

**Key Events**:
1. Enter Friendly Town; meet the townsfolk
2. Visit the saloon (social hub), shop, and medical station
3. Learn about the wasteland's faction dynamics and major threats
4. Accept the first formal quest
5. Trade, repair, and resupply

**NPCs**:
- **Mayor Ma**: A shrewd, practical settlement leader — friendly to newcomers but cautious
- **Ironhand the Smith**: Weapons and armor merchant — few words, excellent craftsmanship
- **Daisy the Bartender**: The gossip hub — knows most rumors circulating the wasteland
- **Minutemen Liaison**: Invites players to join the Minutemen and offers quests

**Available Quests**:
Pick from these quests or improvise based on the story:
1. **Clear the nearby ruins of Raiders** — combat-oriented, reward: weapons + Caps
2. **Help repair the water purifier** — exploration-oriented, reward: settlement favor + supplies
3. **Investigate the missing caravan** — investigation-oriented, reward: clues + Caps

**GM Notes**:
- This is primarily a social/exploration chapter
- Let players explore freely; don't rush the plot
- Every NPC should reveal some piece of wasteland information
- Generate shop inventory with `python3 scripts/fallout_game.py loot common 5`

**Ending Trigger**: Accept a quest and leave Friendly Town

---

### Chapter 4: The First Quest

**Setting**: Depends on the quest chosen in Chapter 3

**Structure** (general):
1. Travel encounters (use random events)
2. Arrive at the quest objective area
3. Explore / search
4. Core challenge (combat / negotiation / puzzle)
5. Moral choice (gray area)
6. Consequences of success or failure

**Moral Dilemma Examples**:
- The Raider hideout holds captive civilians, but the Raiders claim they're con artists
- The water purifier part is in a small settlement that's already using it
- The missing caravan is held by Super Mutants, but the mutants say the traders chose to stay

**GM Notes**:
- Difficulty should be noticeably higher than Chapter 2
- Include at least one section that requires multi-player cooperation
- Moral choices have no "correct answer"
- Adjust player reputation based on outcomes

**Ending Trigger**: Quest completed or definitively failed

---

### Chapter 5: Faction Politics

**Setting**: Multiple faction strongholds

**Key Events**:
1. Return to Friendly Town; report quest results
2. Attract faction attention (Brotherhood of Steel or Minutemen contact the players)
3. Visit a faction stronghold; learn their ideology
4. Complete a faction test mission
5. Make a preliminary alignment choice

**Faction Quests**:
- **Brotherhood of Steel**: Recover pre-war technology, exterminate mutated creatures
- **The Minutemen**: Establish new settlements, protect civilians
- **The Railroad**: Help synths escape
- **Independent Path**: Join no one, go it alone

**GM Notes**:
- Let players fully understand each faction's pros and cons
- If players choose different factions, this becomes interesting internal conflict
- Don't force a choice — hint that "neutral" is also an option

**Ending Trigger**: Faction alignment chosen or clearly declared

---

### Chapter 6+: Open World

Based on player alignment and choices, the GM should:

1. **Advance the main quest**: Follow the faction's main questline
2. **World reactivity**: NPCs change attitude based on player reputation and choices
3. **Level up**: After each chapter, reward skill points with `skill-up`
4. **New regions**: Unlock more distant wasteland areas (the Glowing Sea, Diamond City)
5. **Endgame setup**: The final quest involves the Institute's secrets

### Pacing Rules
- Every 2-3 turns should feature a significant event or plot twist
- Never go more than 3 consecutive turns without combat or a major encounter
- After each chapter, let players rest, trade, and upgrade
- Alternate pacing: exploration → combat → social → combat → major choice

---

## NPC Quick Generator

Use the game engine to generate random NPCs:

```
exec: python3 scripts/fallout_game.py npc-gen
exec: python3 scripts/fallout_game.py npc-gen 3
```

Each generated NPC includes:
- **Name**: Wasteland-style name
- **Appearance**: [build] + [distinguishing feature] + [clothing]
- **Motive**: [need] + [method] + [obstacle]
- **Knowledge**: At least one piece of information useful to the players
- **Speech style**: [terse / chatty / crude / refined / cryptic]

Example:
- **Rust Sledge**: A tall, hunched man with a gold tooth, wearing a grimy merchant's duster. He wants to escape Raider pursuit but needs safe passage. He knows the location of a hidden water source nearby. Speaks bluntly, no wasted words.

---

## Character Creation Presets

Each preset has a recommended SPECIAL distribution (total 40) and 3 tag skills. Players can accept the preset or redistribute points and pick their own tag skills.

**Vault Dweller** — Balanced tech specialist
STR 4 · PER 7 · END 5 · CHA 4 · INT 8 · AGI 6 · LCK 6
Tag: Science, Lockpick, Small Guns

**Wasteland Wanderer** — Tough and stealthy survivor
STR 5 · PER 6 · END 7 · CHA 4 · INT 5 · AGI 7 · LCK 6
Tag: Survival, Sneak, Melee

**Caravan Guard** — Frontline fighter and trader
STR 7 · PER 7 · END 6 · CHA 4 · INT 4 · AGI 6 · LCK 6
Tag: Small Guns, Repair, Barter

**Smooth Talker** — Charismatic negotiator and trader
STR 4 · PER 6 · END 5 · CHA 8 · INT 6 · AGI 6 · LCK 5
Tag: Speech, Barter, Medicine

**Field Medic** — Lucky battlefield medic
STR 4 · PER 6 · END 7 · CHA 4 · INT 7 · AGI 5 · LCK 7
Tag: Medicine, Science, Survival

**Drifter** — Nimble thief and scavenger
STR 4 · PER 7 · END 4 · CHA 5 · INT 5 · AGI 8 · LCK 7
Tag: Lockpick, Sneak, Barter
