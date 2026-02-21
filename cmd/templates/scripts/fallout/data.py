"""Pure data tables for the Fallout RPG game engine."""

# ---------------------------------------------------------------------------
# Encounters (87 total across 5 categories)
# ---------------------------------------------------------------------------

ENCOUNTERS = {
    "wasteland": [
        {"name": "Wandering Trader", "type": "npc", "desc": "A merchant hauling a massive pack, leading a brahmin by a frayed rope. He shakes a brass bell to attract customers.", "difficulty": 0},
        {"name": "Radroach Swarm", "type": "combat", "desc": "A swarm of mutated cockroaches pours from a crack in the earth, hissing as they skitter toward you.", "difficulty": 1},
        {"name": "Abandoned Campsite", "type": "explore", "desc": "A derelict campsite sits by the roadside. The tent is shredded, but there might still be salvageable supplies.", "difficulty": 0},
        {"name": "Super Mutant Patrol", "type": "combat", "desc": "Heavy footsteps echo in the distance. Two Super Mutants lumber along on patrol, steel pipes resting on their shoulders.", "difficulty": 3},
        {"name": "Mole Rat Den", "type": "combat", "desc": "A mole rat bursts from the dirt, shrieking loud enough to split eardrums.", "difficulty": 2},
        {"name": "Wasteland Drifter", "type": "npc", "desc": "A drifter in cracked leather rides a brahmin at a lazy pace. He doesn't look friendly.", "difficulty": 1},
        {"name": "Collapsed Signal Tower", "type": "explore", "desc": "A toppled radio tower lies in a tangle of steel. Useful electronic components might still be buried in the wreckage.", "difficulty": 1},
        {"name": "Deathclaw", "type": "combat", "desc": "A Deathclaw explodes from the brush! Its razor talons gleam cold in the sunlight.", "difficulty": 4},
        {"name": "Stray Dog", "type": "npc", "desc": "A gaunt, half-starved dog wanders among the ruins. It spots you and wags its tail hopefully.", "difficulty": 0},
        {"name": "Brotherhood of Steel Scout", "type": "npc", "desc": "A Brotherhood Knight in power armor appears ahead, scanning the wasteland through binoculars.", "difficulty": 2},
        {"name": "Raider Ambush", "type": "combat", "desc": "Gunfire erupts from behind a row of rusted cars! A gang of Raiders has set an ambush.", "difficulty": 2},
        {"name": "Wounded Traveler", "type": "npc", "desc": "A wounded traveler lies by the road, calling out weakly for help. Could be a trap, could be genuine.", "difficulty": 1},
        {"name": "Feral Ghoul Pack", "type": "combat", "desc": "A pack of feral ghouls pours out of a ruined building, howling with bone-chilling fury.", "difficulty": 2},
        {"name": "Abandoned Military Truck", "type": "explore", "desc": "A rust-eaten military truck lies overturned on the roadside. The lock on the rear cargo hold is loose.", "difficulty": 1},
        {"name": "Rad Storm Incoming", "type": "hazard", "desc": "The sky turns an ominous green. A radiation storm is closing in fast — find shelter immediately.", "difficulty": 2},
        {"name": "Minutemen Patrol", "type": "npc", "desc": "A handful of Minutemen in makeshift armor patrol the area, protecting a nearby settlement.", "difficulty": 0},
        {"name": "Radscorpion", "type": "combat", "desc": "A giant radscorpion erupts from the sand, its venomous stinger raised high.", "difficulty": 3},
        {"name": "Abandoned Bunker Entrance", "type": "explore", "desc": "A metal hatch pokes through the scrub grass, leading underground. The lock is caked in dust.", "difficulty": 2},
        {"name": "Radstag", "type": "npc", "desc": "A mutated two-headed deer stands in the distance, watching you warily. Its meat could fill a belly.", "difficulty": 1},
        {"name": "Trap Zone", "type": "hazard", "desc": "The ground is littered with traps — landmines, tripwires, and deadfall rigs. Proceed with extreme caution.", "difficulty": 2},
        {"name": "Mysterious Stranger Merchant", "type": "npc", "desc": "A trench-coated figure materializes out of nowhere, displaying an array of bizarre goods. Prices are outrageous, but the merchandise is genuine.", "difficulty": 0},
        {"name": "Bomb Collar Victim", "type": "hazard", "desc": "A terrified man stumbles toward you — Raiders strapped a bomb to his chest! The timer is ticking.", "difficulty": 3},
        {"name": "Abandoned Nuka-Cola Plant", "type": "explore", "desc": "A massive factory sign still bears the faded Nuka-Cola logo. The assembly line has been silent for two centuries, but there might be stock left inside.", "difficulty": 1},
        {"name": "Mutant Flora Zone", "type": "hazard", "desc": "The road ahead is choked with enormous mutated vines, some still slowly writhing. The air is thick with sickeningly sweet pollen.", "difficulty": 2},
        {"name": "Brahmin Herder", "type": "npc", "desc": "An old herder drives a small brahmin herd across the wastes. He looks like he knows every back trail in the region.", "difficulty": 0},
        {"name": "Railroad Dead Drop", "type": "npc", "desc": "A hurried figure weaves through the ruins. You notice them leaving a strange symbol scratched into a wall.", "difficulty": 1},
        {"name": "Institute Synths", "type": "combat", "desc": "Blue light flashes and two figures in black uniforms teleport in, clutching laser weapons. Institute Synths.", "difficulty": 3},
        {"name": "Pre-War Military Checkpoint", "type": "explore", "desc": "Sandbag walls, a rusted APC, skeletons in fatigues — an abandoned military checkpoint. The ammo crates might not be picked clean yet.", "difficulty": 1},
        {"name": "Irradiated Water Source", "type": "hazard", "desc": "The puddle ahead glows an ominous green. Your Geiger counter screams. Detour or risk wading through.", "difficulty": 1},
        {"name": "Crashed Vertibird", "type": "explore", "desc": "A Brotherhood Vertibird lies crumpled in the dirt, wreckage scattered wide. Power armor fragments and high-tech weapon parts glint among the debris.", "difficulty": 2},
        {"name": "Wasteland Arena", "type": "npc", "desc": "Cheers roar in the distance. A makeshift arena hosts gladiatorial bouts between wasteland fighters. Someone is recruiting challengers.", "difficulty": 2},
        {"name": "Deathclaw Nest", "type": "combat", "desc": "Massive claw marks and gnawed bones litter the ground. A deep, rumbling howl echoes from a cave ahead — this is a Deathclaw nest.", "difficulty": 5},
        {"name": "Abandoned Gas Station", "type": "explore", "desc": "A gas station converted into a makeshift shelter. Sheet-metal walls with firing slits, but it seems deserted now.", "difficulty": 1},
        {"name": "Children of Atom Preacher", "type": "npc", "desc": "A figure in white robes stands atop the rubble, preaching the glory of the Atom. Their devotion to radiation borders on fanatical.", "difficulty": 0},
    ],
    "urban": [
        {"name": "Sniper", "type": "combat", "desc": "A glint of light flashes from an upper-floor window of a ruined high-rise — sniper!", "difficulty": 3},
        {"name": "Metro Station Entrance", "type": "explore", "desc": "A partially collapsed metro station entrance. Strange sounds echo from the darkness below.", "difficulty": 2},
        {"name": "Nuka-Cola Vending Machine", "type": "explore", "desc": "A Nuka-Cola vending machine on a street corner — and it's still humming!", "difficulty": 0},
        {"name": "Ghoul Settlement", "type": "npc", "desc": "A small community of friendly ghouls has set up shop. They watch you warily.", "difficulty": 1},
        {"name": "Raider Stronghold", "type": "combat", "desc": "A ruined building ahead has been fortified into a Raider stronghold. Skulls hang outside as a warning.", "difficulty": 3},
        {"name": "Abandoned Hospital", "type": "explore", "desc": "A derelict hospital. Medical supplies and chems might still be inside.", "difficulty": 2},
        {"name": "Protectron Shop Clerk", "type": "npc", "desc": "A Protectron still faithfully mans its post, greeting you with a tinny mechanical voice. Welcome to the shop.", "difficulty": 0},
        {"name": "Graffiti Wall", "type": "explore", "desc": "The graffiti on this wall looks like coded markers or a map, pointing to somewhere nearby.", "difficulty": 1},
        {"name": "Elevator Shaft Collapse", "type": "hazard", "desc": "The floor at the end of the corridor gives way, revealing a gaping elevator shaft below!", "difficulty": 2},
        {"name": "Survivor Hideout", "type": "npc", "desc": "Deep in the building you find a small survivor camp — a handful of people huddled around a tiny fire.", "difficulty": 1},
        {"name": "Abandoned School", "type": "explore", "desc": "A collapsed school building. Textbooks and supplies litter the classrooms. The basement door is locked tight, and faint sounds come from within.", "difficulty": 2},
        {"name": "Super Mutant Stronghold", "type": "combat", "desc": "The top floors of a ruined building are held by Super Mutants. Human bones dangle from the balcony as decor. A Suicider patrols the perimeter.", "difficulty": 3},
        {"name": "Pre-War Bank Vault", "type": "explore", "desc": "A pre-war bank. The massive vault door stands ajar. Inside are stacks of worthless pre-war currency — but the safe deposit boxes might hold something valuable.", "difficulty": 2},
        {"name": "Haywire Protectron", "type": "combat", "desc": "A Protectron patrols the street, blaring through its speaker: 'VIOLATIONS WILL BE MET WITH LETHAL FORCE!' Its weapons systems appear operational.", "difficulty": 2},
        {"name": "Abandoned Radio Station", "type": "explore", "desc": "A radio tower still broadcasting a signal. The recording equipment inside is intact, playing a pre-war DJ's show on loop. The transmitter might be repairable.", "difficulty": 2},
        {"name": "Rooftop Garden", "type": "explore", "desc": "Someone converted a rooftop into a garden. Mutant crops thrive under radioactive rain. Whoever tended them is long gone.", "difficulty": 0},
        {"name": "Sewer Entrance", "type": "explore", "desc": "A manhole cover has been pried open. The dark sewer below reeks. Rumor has it the tunnels connect to the far side of the city — if you survive the crossing.", "difficulty": 2},
        {"name": "Turret Emplacement", "type": "combat", "desc": "Two automated turrets guard the intersection. Red laser triplines sweep back and forth across the pavement, still faithfully executing two-hundred-year-old security protocols.", "difficulty": 2},
        {"name": "Railroad Markings", "type": "explore", "desc": "Several walls are covered in Railroad ciphers. Decoding these symbols might reveal a nearby safehouse.", "difficulty": 1},
    ],
    "vault": [
        {"name": "Malfunctioning Robot", "type": "combat", "desc": "A maintenance bot in the corridor suddenly glitches — red lights flashing — and charges at you.", "difficulty": 1},
        {"name": "Flooded Section", "type": "hazard", "desc": "The corridor ahead is half-flooded with irradiated water, its surface glowing faintly green.", "difficulty": 1},
        {"name": "Terminal Logs", "type": "explore", "desc": "A still-functioning terminal displays the vault's final log entries on a flickering screen.", "difficulty": 1},
        {"name": "Hidden Room", "type": "explore", "desc": "A barely noticeable crack in the wall. There seems to be a concealed space behind it.", "difficulty": 2},
        {"name": "Radroach Nest", "type": "combat", "desc": "A rustling sound from the ventilation ducts — then radroaches pour out in a chittering flood.", "difficulty": 1},
        {"name": "Overloaded Reactor", "type": "hazard", "desc": "The reactor emits an abnormal hum. Radiation readings are climbing fast.", "difficulty": 3},
        {"name": "Cryogenic Pod Bay", "type": "explore", "desc": "A row of cryogenic pods, most long since failed. But one is still cycling...", "difficulty": 2},
        {"name": "Overseer's Office", "type": "explore", "desc": "The Overseer's office door is ajar. The safe inside looks untouched.", "difficulty": 2},
        {"name": "Laboratory Remains", "type": "explore", "desc": "A sealed laboratory. Petri dishes and test tubes litter the floor. Terminal records indicate genetic modification experiments were conducted here.", "difficulty": 2},
        {"name": "Rogue Clones", "type": "combat", "desc": "At the end of the corridor, a figure in a vault suit appears — no, two, three identical figures! They stagger toward you with blank, dead eyes.", "difficulty": 2},
        {"name": "Vault AI", "type": "npc", "desc": "A wall speaker crackles to life: 'Welcome back, Resident. You have been absent for... ERROR... days. Please report to the atrium for your annual physical.' The vault's AI is still running.", "difficulty": 1},
        {"name": "Water System Failure", "type": "hazard", "desc": "Pipes have burst, flooding half the corridor with irradiated water. Worse, something is moving beneath the surface.", "difficulty": 2},
        {"name": "Armory", "type": "explore", "desc": "A heavy door labeled 'ARMORY' stands slightly ajar. Weapons still hang on the racks inside, but the security system's red light is still blinking.", "difficulty": 3},
        {"name": "Cryopod Survivor", "type": "npc", "desc": "A cryopod hisses and begins thawing. A terrified person tumbles out, utterly disoriented. They still think it's 2077.", "difficulty": 0},
    ],
    "interior": [
        {"name": "Trapped Corridor", "type": "hazard", "desc": "The hallway is laced with tripwires and jury-rigged explosives. Careful disarming or a detour is needed.", "difficulty": 2},
        {"name": "Creature Lair", "type": "combat", "desc": "You've stumbled into some mutant creature's lair. Bones and torn cloth are scattered everywhere.", "difficulty": 2},
        {"name": "Locked Safe", "type": "explore", "desc": "A heavy safe sits in the corner. The lock looks formidable.", "difficulty": 2},
        {"name": "Password-Locked Terminal", "type": "explore", "desc": "A terminal requiring a password. Hacking it might yield valuable intel.", "difficulty": 2},
        {"name": "Floor Collapse", "type": "hazard", "desc": "The floor gives way beneath you — you barely avoid plunging to the level below!", "difficulty": 1},
        {"name": "Friendly Survivor", "type": "npc", "desc": "A survivor hiding in the depths. They know the layout of this place.", "difficulty": 0},
        {"name": "Secret Door", "type": "explore", "desc": "A hidden door behind a bookshelf, carefully disguised. Pulling a specific book causes it to swing open, revealing a secret room.", "difficulty": 2},
        {"name": "Mutant Rat Swarm", "type": "combat", "desc": "Squeaking erupts from the shadows. A swarm of fist-sized mutant rats floods out. One is harmless — but there are hundreds.", "difficulty": 1},
        {"name": "Pre-War Journal", "type": "explore", "desc": "A well-preserved journal on the desk chronicles the previous owner's final days. Between the lines may lie passwords or clues to hidden locations.", "difficulty": 1},
        {"name": "Cave-In Zone", "type": "hazard", "desc": "The ceiling groans ominously. Rubble rains down in chunks. This section could collapse at any moment — move fast or find another route.", "difficulty": 2},
        {"name": "Escaped Experiment", "type": "combat", "desc": "An iron cage has been torn open from the inside. The tag beside it reads 'Subject-07'. Heavy breathing and claws scraping on concrete echo from deeper in.", "difficulty": 3},
        {"name": "Hidden Supply Cache", "type": "explore", "desc": "Beneath the floorboards, a sealed supply cache. The crates bear pre-war government seals and appear never to have been opened.", "difficulty": 1},
    ],
    "special": [
        {"name": "Shattered Dreams Cafe", "type": "npc", "desc": "An impossible diner with patrons who speak in half-sentences. They recount events that never happened and cite histories that don't exist.", "difficulty": 0},
        {"name": "Crashed Alien Ship", "type": "explore", "desc": "A small spacecraft lies crumpled in the dirt. Green fluid seeps from the hull. Strange weapons and alien crystals are scattered among the debris.", "difficulty": 3},
        {"name": "Temporal Rift", "type": "explore", "desc": "Stones arranged in a ring hum faintly. Stepping inside, the world flickers — for a heartbeat you glimpse the world before the bombs fell.", "difficulty": 2},
        {"name": "Knights of the Holy Hand Grenade", "type": "npc", "desc": "Five warriors in anachronistic plate armor call themselves knights, questing for the legendary 'Holy Hand Grenade.' They'll pay 500 Caps for your help.", "difficulty": 1},
        {"name": "Bridge Keeper", "type": "npc", "desc": "A robed figure guards a bridge, declaring that only those who answer three riddles may pass. The consequences of failure are dire.", "difficulty": 2},
        {"name": "Six Old Ladies", "type": "combat", "desc": "Six elderly women armed with rolling pins burst from the ruins. Do not underestimate them — their combat prowess is terrifying.", "difficulty": 2},
        {"name": "Dueling Robots", "type": "combat", "desc": "Two heavily armed robots are locked in mortal combat. Sparks fly and shrapnel rains down. The winner may turn its attention to you.", "difficulty": 3},
        {"name": "Alien Blaster Drop", "type": "explore", "desc": "A streak of light blazes across the sky, and debris showers down like a meteor storm. In the impact crater, you find a weapon pulsing with eerie blue energy.", "difficulty": 1},
    ],
}

# ---------------------------------------------------------------------------
# Loot Tables (134 items across 5 tiers)
# ---------------------------------------------------------------------------

LOOT_TABLES = {
    "junk": [
        "Scrap Metal x3", "Screws x5", "Duct Tape", "Glass Shards x2", "Cloth Scraps",
        "Empty Bottle", "Shell Casings x4", "Rusty Wire", "Broken Circuit Board", "Empty Mag",
        "Splintered Wood x2", "Plastic Shards", "Old Newspaper", "Bent Coins x3", "Aluminum Can",
        "Abraxo Cleaner", "Alarm Clock", "Camera", "Clipboard", "Coffee Cup",
        "Cooking Pot", "Desk Fan", "Duct Tape", "Fire Extinguisher", "Frying Pan",
        "Light Bulb", "Magnifying Glass", "Metal Bucket", "Paint Can", "Picture Frame",
        "Plunger", "Soap", "Tea Kettle", "Teddy Bear", "Telephone",
        "Toaster", "Vase", "Vacuum Tube", "Wrench", "Blowtorch",
        "Fuse", "Hammer", "Pencil", "Screwdriver", "Shovel",
        "Handcuffs", "Pre-War Money", "Baseball", "Bowling Ball", "Marble",
        "Toy Car", "Typewriter", "Copper Wire x3", "Spring x2", "Gear x2",
    ],
    "common": [
        "Stimpak", "10mm Ammo x12", "Purified Water x2", "RadAway", "Nuka-Cola",
        "Mutant Jerky", "Squirrel on a Stick x2", "Bandages", "Flashlight", "Worn Toolbox",
        "Bubblegum", "Magazine", "Lead Pipe", "Bobby Pins x3", "Flint Lighter",
        ".38 Rounds x15", "Shotgun Shells x6", "Leather Armor (Damaged)", "Metal Armor Scrap",
        "Cram", "BlamCo Mac & Cheese", "Instant Mashed Potatoes", "Salisbury Steak",
        "Sugar Bombs", "Fancy Lads Snack Cakes", "Dandy Boy Apples", "Mutfruit",
        "Beer", "Bourbon", "Fireworks", "Rad-X",
    ],
    "uncommon": [
        "Fusion Cell x8", "Psycho", "Stealth Boy", "Nuka-Cola Quantum",
        "Combat Rifle Ammo x20", "Power Fist", "Leather Armor Piece", "Repair Kit",
        "Doctor's Bag", "RadAway x2", "Night-Vision Goggles", "Military Rations x3",
        "Nuka-Cherry", "Combat Armor Piece", "5.56mm Ammo x30",
        ".50 Cal Ammo x10", "Plasma Cartridge x6", "Fusion Cell x4",
        "Mentats", "Psycho", "Med-X", "Buffout",
        "Hunting Rifle", "Combat Rifle", "Laser Pistol",
        "Grognak the Barbarian Comic", "Guns and Bullets Magazine", "Tesla Science Magazine",
    ],
    "rare": [
        "Power Armor Piece", "2mm EC x5", "Mini Nuke",
        "Stealth Device", "Energy Weapon Mod", "Pre-War Money x50",
        "Technical Documents", "Rare Chems", "Full Tool Kit", "Military-Grade Armor",
        "2mm EC x10", "Missile x2", "Fusion Core",
        "Full Combat Armor Set", "Laser Rifle (Modded)", "Plasma Rifle",
        "Ballistic Fiber", "Nuclear Material x3", "Fiber Optics x5",
        "U.S. Covert Operations Manual", "Wasteland Survival Guide",
    ],
    "unique": [
        "Vault-Tec Blueprint", "Improved Stimpak Formula", "Legendary Weapon Mod",
        "Power Armor Core", "Brotherhood of Steel Holotape", "Pre-War Map (Marked Locations)",
        "Anomalous Crystal", "Wasteland Legend's Journal", "Super Mutant Transmitter",
        "Perfectly Preserved Pie", "Institute Relay Device", "Alien Blaster",
        "X-01 Power Armor Helmet", "Legendary Shotgun (The Scattergun)",
    ],
}

# ---------------------------------------------------------------------------
# Atmospheric Events (21)
# ---------------------------------------------------------------------------

ATMOSPHERIC = [
    {"event": "Rad Storm", "desc": "The sky turns a sickly green as radioactive particles ride the wind. Everyone takes +50 rads per round until shelter is found.", "severity": "high"},
    {"event": "Dust Storm", "desc": "A wall of yellow dust blots out the horizon. PER checks at -2.", "severity": "medium"},
    {"event": "Acid Rain", "desc": "Corrosive rain begins to fall from the sky. Exposed flesh takes slow, steady damage.", "severity": "medium"},
    {"event": "Calm Night", "desc": "A rare, peaceful night in the wasteland. The stars are impossibly bright. Safe to rest.", "severity": "none"},
    {"event": "Dense Fog", "desc": "Thick fog blankets the area. Visibility is near zero. Sneak +2, but navigation is treacherous.", "severity": "low"},
    {"event": "Extreme Heat", "desc": "Temperatures skyrocket. Metal surfaces blister to the touch. END check or -5 HP per round.", "severity": "medium"},
    {"event": "Radioactive Aurora", "desc": "A magnificent aurora of radiation lights up the wasteland sky. Great visibility, but rads tick up slightly.", "severity": "low"},
    {"event": "Insect Migration", "desc": "A droning buzz fills the air. A massive swarm of bloatflies is migrating overhead. Do not attract their attention.", "severity": "low"},
    {"event": "Aftershock", "desc": "The ground trembles underfoot. Distant buildings crumble. Watch for unstable structures.", "severity": "medium"},
    {"event": "Electromagnetic Pulse", "desc": "A blinding flash across the sky. All electronic devices go dark.", "severity": "high"},
    {"event": "Acid Rain (Severe)", "desc": "Yellow-green rain pours from a diseased sky. It burns exposed skin and corrodes metal. Shelter is imperative.", "severity": "high"},
    {"event": "Black Rain", "desc": "Oily black rain falls from thunderheads, leaving dark stains and reeking of chemicals. Prolonged exposure causes nausea.", "severity": "medium"},
    {"event": "Sudden Cold Snap", "desc": "Temperature plummets without warning. Breath turns to mist, water sources freeze, exposed skin goes numb.", "severity": "medium"},
    {"event": "Dead Silence", "desc": "All sound vanishes. No wind, no insects, no distant gunfire. The wasteland holds its breath. Something is watching.", "severity": "low"},
    {"event": "Wasteland Aurora", "desc": "Curtains of green and violet light drift across the night sky — radiation interacting with the damaged atmosphere. Eerie and beautiful.", "severity": "low"},
    {"event": "Eyebot Broadcast", "desc": "A roaming Eyebot drifts past, blaring pre-war advertisements and government propaganda at full volume. The noise may attract unwanted visitors.", "severity": "low"},
    {"event": "Mirage", "desc": "Heat shimmer conjures the image of an intact city on the horizon — skyscrapers, lights, everything. It dissolves as you approach.", "severity": "none"},
    {"event": "Insect Migration (Massive)", "desc": "The sky darkens as an enormous swarm of bloatflies or radroaches passes overhead. They aren't attacking — they're heading somewhere. What's drawing them?", "severity": "low"},
    {"event": "Mysterious Radio Signal", "desc": "A new frequency appears on the Pip-Boy. A distorted voice repeats coordinates, then a woman's scream, then silence. It loops every three minutes.", "severity": "low"},
    {"event": "Ash Storm", "desc": "Thick gray smoke from a distant blaze rolls across the area. Visibility drops and breathing without protection becomes difficult.", "severity": "medium"},
    {"event": "Blood Storm", "desc": "The sky turns blood-red, a massive vortex forming overhead. Howling winds and incessant lightning. Something is very wrong.", "severity": "high"},
]

# ---------------------------------------------------------------------------
# Quest Hooks (20)
# ---------------------------------------------------------------------------

QUEST_HOOKS = [
    {"quest": "The Missing Caravan", "desc": "A trade caravan vanished on the road to Diamond City. The families are offering a bounty for answers.", "reward": "300 Caps + Caravan supplies"},
    {"quest": "Purifier Parts", "desc": "The settlement's water purifier broke down. Replacement parts must be salvaged from an abandoned factory.", "reward": "Clean water supply + Reputation"},
    {"quest": "Mutant Kidnapping", "desc": "Super Mutants dragged off several settlers. Word is they're being held in a nearby stronghold.", "reward": "200 Caps + Settlement support"},
    {"quest": "The Radio Signal", "desc": "A mysterious radio transmission has been picked up, apparently originating from a pre-war military installation.", "reward": "Military gear + Intel"},
    {"quest": "Plague Outbreak", "desc": "A strange plague is spreading through the settlement. Medicine must be found or the source investigated.", "reward": "Medical supplies + Research data"},
    {"quest": "Raider Shakedown", "desc": "A Raider gang is demanding protection money from the settlement. Negotiate or take them out.", "reward": "Security + Seized weapons"},
    {"quest": "Pre-War Stash", "desc": "A tattered map has surfaced, seemingly pointing to a secret pre-war supply depot.", "reward": "Rare supplies"},
    {"quest": "Scientist Escort", "desc": "A scientist needs safe passage to a distant research facility. The road is crawling with hostiles.", "reward": "Tech upgrades + Intel"},
    {"quest": "Arena Invitation", "desc": "An invitation to the wasteland fighting arena has arrived. Champions walk away with serious loot.", "reward": "Caps + Reputation + Gear"},
    {"quest": "The Communication Network", "desc": "Someone wants to build a wasteland communication network. Several relay towers need repair.", "reward": "Comm equipment + Intelligence network"},
    {"quest": "The Glowing Well", "desc": "A small farm's well is emitting an eerie green glow. A Glowing One is trapped at the bottom. The settlers want it gone but won't go down themselves.", "reward": "Farm reputation + Supplies"},
    {"quest": "Water Rights", "desc": "Escaped slaves and a ghoul settlement are feuding over a water purification point. Both sides claim it's theirs.", "reward": "Peace accord + Reputation with both factions"},
    {"quest": "Synth Accusation", "desc": "Two identical people stand in the street, each accusing the other of being a Synth. A crowd gathers. Someone has to make the call.", "reward": "The truth + Reputation"},
    {"quest": "Treasure Map", "desc": "A crude map was found on a dead scavenger, marked with coordinates and the word 'TREASURE.' The map is torn, but the heading is clear.", "reward": "Rare loot"},
    {"quest": "The Reluctant Ghoul", "desc": "A ghoul wants to settle in a human community but fears rejection. He paces outside the gates, needing someone to vouch for him.", "reward": "Ghoul reputation + Unique goods"},
    {"quest": "Legend of the Fire Gecko", "desc": "Hunters around the campfire speak of a legendary Fire Gecko — a beast whose hide fetches a king's ransom. They know roughly where its den is.", "reward": "Fire Gecko Hide (extremely valuable) + Reputation"},
    {"quest": "The Twisted Armor", "desc": "A corpse in warped metal armor lies in the dust. The metal is bent into impossible blue-pink patterns, the skin beneath horribly burned. What weapon did this?", "reward": "Weapon lead + Tech intel"},
    {"quest": "Prisoner Rescue", "desc": "Super Mutants are holding a prisoner in a crude cage. The captive begs for help. Rescue means a fight — unless you find another way.", "reward": "200 Caps + Intel"},
    {"quest": "Wasteland Funeral", "desc": "A small group of settlers holds a funeral in the open wastes. They're vulnerable during the ceremony and ask for someone to stand watch.", "reward": "Settlement reputation + Supplies"},
    {"quest": "Power Plant Restart", "desc": "Rumor says the old nuclear power plant on the outskirts can be brought back online. Electricity for the whole wasteland — if you survive the rads and the mutants inside.", "reward": "Settlement power + Major reputation"},
]

# ---------------------------------------------------------------------------
# Consumable Effects (15)
# ---------------------------------------------------------------------------

CHEM_EFFECTS = {
    "Stimpak": {"heal": 15, "desc": "Restores 15 HP"},
    "Super Stimpak": {"heal": 30, "desc": "Restores 30 HP"},
    "RadAway": {"rads": -100, "desc": "Removes 100 rads"},
    "Rad-X": {"effect": "Rad Resistance", "duration": 3, "desc": "Halves radiation intake for 3 rounds"},
    "Nuka-Cola": {"heal": 2, "ap": 1, "rads": 5, "desc": "Restores 2 HP, +1 AP, +5 rads"},
    "Nuka-Cola Quantum": {"heal": 10, "ap": 5, "rads": 10, "desc": "Restores 10 HP, +5 AP, +10 rads"},
    "Purified Water": {"heal": 5, "desc": "Restores 5 HP"},
    "Dirty Water": {"heal": 2, "rads": 15, "desc": "Restores 2 HP, +15 rads"},
    "Psycho": {"effect": "Rage", "duration": 3, "stat_mods": {"END": 1}, "desc": "DMG +3, END +1 for 3 rounds. Addiction risk"},
    "Jet": {"effect": "Jet Rush", "duration": 2, "stat_mods": {"AGI": 2}, "desc": "AGI +2, extra action for 2 rounds. Addiction risk"},
    "Mentats": {"effect": "Enhanced Cognition", "duration": 3, "stat_mods": {"INT": 2, "PER": 2}, "desc": "INT +2, PER +2 for 3 rounds. Addiction risk"},
    "Med-X": {"effect": "Pain Suppression", "duration": 3, "desc": "Damage taken -2 for 3 rounds. Addiction risk"},
    "Buffout": {"effect": "Hulking", "duration": 3, "stat_mods": {"STR": 3, "END": 3}, "desc": "STR +3, END +3 for 3 rounds. Addiction risk"},
    "Mutant Jerky": {"heal": 3, "rads": 5, "desc": "Restores 3 HP, +5 rads"},
    "Squirrel on a Stick": {"heal": 2, "desc": "Restores 2 HP"},
}

# ---------------------------------------------------------------------------
# Enemy Templates
# ---------------------------------------------------------------------------

ENEMY_TEMPLATES = {
    # Tier 1 — Early game (wasteland pests)
    "Radroach":              {"tier": 1, "hp": 10, "damage": "1d6",  "attack_skill": 8,  "drops": "junk",     "special": ""},
    "Bloatfly":              {"tier": 1, "hp": 10, "damage": "1d6",  "attack_skill": 6,  "drops": "junk",     "special": "Poison spit"},
    "Mole Rat":              {"tier": 1, "hp": 15, "damage": "2d6",  "attack_skill": 10, "drops": "junk",     "special": "Burrow ambush"},
    "Wild Dog":              {"tier": 1, "hp": 15, "damage": "2d6",  "attack_skill": 12, "drops": "junk",     "special": "Pack tactics"},
    # Tier 2 — Early-mid (humanoids, basic mutants)
    "Feral Ghoul":           {"tier": 2, "hp": 20, "damage": "2d6",  "attack_skill": 12, "drops": "common",   "special": "Radiation immunity"},
    "Raider":                {"tier": 2, "hp": 25, "damage": "3d6",  "attack_skill": 10, "drops": "common",   "special": ""},
    "Raider Psycho":         {"tier": 2, "hp": 25, "damage": "3d6",  "attack_skill": 12, "drops": "common",   "special": "Berserk (ignores pain)"},
    "Mirelurk Hatchling":    {"tier": 2, "hp": 15, "damage": "2d6",  "attack_skill": 10, "drops": "common",   "special": "Shell armor"},
    # Tier 3 — Mid game
    "Super Mutant":          {"tier": 3, "hp": 40, "damage": "4d6",  "attack_skill": 12, "drops": "uncommon", "special": "Radiation immunity"},
    "Raider Veteran":        {"tier": 3, "hp": 35, "damage": "3d6",  "attack_skill": 14, "drops": "common",   "special": ""},
    "Feral Ghoul Reaver":    {"tier": 3, "hp": 35, "damage": "3d6",  "attack_skill": 14, "drops": "uncommon", "special": "Radiation immunity, radiation attack"},
    "Yao Guai":              {"tier": 3, "hp": 45, "damage": "4d6",  "attack_skill": 12, "drops": "uncommon", "special": "Charge attack"},
    "Mirelurk":              {"tier": 3, "hp": 35, "damage": "3d6",  "attack_skill": 12, "drops": "uncommon", "special": "Shell armor"},
    # Tier 4 — Late game
    "Deathclaw":             {"tier": 4, "hp": 60, "damage": "5d6",  "attack_skill": 14, "drops": "rare",     "special": "Armor piercing"},
    "Assaultron":            {"tier": 4, "hp": 50, "damage": "4d6",  "attack_skill": 16, "drops": "rare",     "special": "Laser head attack"},
    "Sentry Bot":            {"tier": 4, "hp": 70, "damage": "5d6",  "attack_skill": 14, "drops": "rare",     "special": "Heavy armor, self-destruct"},
    "Super Mutant Behemoth": {"tier": 4, "hp": 80, "damage": "6d6",  "attack_skill": 12, "drops": "rare",     "special": "AoE attacks"},
    "Mirelurk Queen":        {"tier": 4, "hp": 100, "damage": "6d6", "attack_skill": 14, "drops": "unique",   "special": "Acid spit, spawn hatchlings"},
    # Tier 5 — Boss
    "Legendary Deathclaw":   {"tier": 5, "hp": 90, "damage": "6d6",  "attack_skill": 16, "drops": "unique",   "special": "Regeneration, armor piercing"},
    "Legendary Assaultron":  {"tier": 5, "hp": 70, "damage": "5d6",  "attack_skill": 18, "drops": "unique",   "special": "Stealth, laser head attack"},
}

# ---------------------------------------------------------------------------
# Encounter Rules (chapter-based constraints)
# ---------------------------------------------------------------------------

# max_tier: highest enemy tier allowed
# hp_budget: max total alive enemy HP per encounter (base, scales with player count)
# safe_turns: turns after chapter start where only tier 1 enemies are allowed
ENCOUNTER_RULES = {
    1: {"max_tier": 1, "hp_budget": 30,  "safe_turns": 2},
    2: {"max_tier": 2, "hp_budget": 60,  "safe_turns": 1},
    3: {"max_tier": 2, "hp_budget": 80,  "safe_turns": 1},
    4: {"max_tier": 3, "hp_budget": 120, "safe_turns": 0},
    5: {"max_tier": 4, "hp_budget": 180, "safe_turns": 0},
    6: {"max_tier": 5, "hp_budget": 250, "safe_turns": 0},
}

def hp_to_tier(hp):
    """Derive enemy tier from HP for custom (non-template) enemies."""
    if hp <= 15:
        return 1
    elif hp <= 25:
        return 2
    elif hp <= 45:
        return 3
    elif hp <= 80:
        return 4
    else:
        return 5

# ---------------------------------------------------------------------------
# Weapon Data
# ---------------------------------------------------------------------------

WEAPONS = {
    # Melee weapons
    "Fists":            {"dice": 1, "type": "melee", "special": "Stun on effect"},
    "Knife":            {"dice": 2, "type": "melee", "special": "Bleed on effect"},
    "Pipe Wrench":      {"dice": 2, "type": "melee", "special": "Bleed on effect"},
    "Baseball Bat":     {"dice": 2, "type": "melee", "special": "Knockdown on effect"},
    "Machete":          {"dice": 3, "type": "melee", "special": "Bleed on effect"},
    "Super Sledge":     {"dice": 4, "type": "melee", "special": "Knockdown on effect"},
    "Power Fist":       {"dice": 3, "type": "melee", "special": "Stun on effect"},
    "Ripper":           {"dice": 3, "type": "melee", "special": "Bleed on effect"},
    # Ranged weapons (ammo = ammo item name consumed per shot)
    "Pipe Pistol":      {"dice": 2, "type": "ranged", "special": "",                      "ammo": ".38 Rounds"},
    "10mm Pistol":      {"dice": 3, "type": "ranged", "special": "Pierce on effect",      "ammo": "10mm Ammo"},
    ".44 Magnum":       {"dice": 4, "type": "ranged", "special": "Knockdown on effect",   "ammo": ".44 Ammo"},
    "Hunting Rifle":    {"dice": 4, "type": "ranged", "special": "Knockdown on effect",   "ammo": ".308 Ammo"},
    "Combat Rifle":     {"dice": 4, "type": "ranged", "special": "",                      "ammo": "5.56mm Ammo"},
    "Combat Shotgun":   {"dice": 4, "type": "ranged", "special": "Spread (close: +1d)",   "ammo": "Shotgun Shells"},
    "Laser Pistol":     {"dice": 3, "type": "ranged", "special": "Burn on effect",        "ammo": "Fusion Cell"},
    "Laser Rifle":      {"dice": 4, "type": "ranged", "special": "Burn on effect",        "ammo": "Fusion Cell"},
    "Plasma Rifle":     {"dice": 5, "type": "ranged", "special": "Burn on effect",        "ammo": "Plasma Cartridge"},
    "Minigun":          {"dice": 5, "type": "ranged", "special": "Suppression on effect", "ammo": "5mm Ammo"},
    "Missile Launcher": {"dice": 6, "type": "ranged", "special": "Knockdown + AoE",       "ammo": "Missile"},
    "Fat Man":          {"dice": 8, "type": "ranged", "special": "AoE + Radiation",       "ammo": "Mini Nuke"},
}

# ---------------------------------------------------------------------------
# NPC Generator Data
# ---------------------------------------------------------------------------

NPC_SURNAMES = [
    "Six-Finger", "One-Eye", "Old", "Iron", "Copper", "Red", "Black", "Dusty", "Scrap", "Bullet",
    "Razor", "Bones", "Grease", "Tin", "Flint", "Salt", "Ash", "Cinder", "Rust", "Patch",
]

NPC_NAMES = [
    "Pete", "Rusty", "Doc", "Skeeter", "Mack", "Baldy", "Squint", "Shadow",
    "Pox", "Gimpy", "Scruff", "Boulder", "Steeljaw", "Quickdraw", "Stilts",
    "Mumbles", "Tiny", "Beanpole", "Matchstick", "Scorpion", "Snake-Eyes", "Sledge",
]

NPC_BUILDS = ["brawny", "gaunt", "short and wiry", "tall and hunched", "stocky", "scrawny", "solidly built"]

NPC_FEATURES = [
    "a knife scar across his face", "one eye", "a bald head tanned dark", "a face full of freckles",
    "a pair of welding goggles", "half an ear missing", "an old wound at the corner of his mouth",
    "radiation burns on his forehead", "a tattoo on his left arm", "a crooked nose",
    "white hair but a young face", "a gold tooth glinting in his mouth",
]

NPC_CLOTHES = [
    "cobbled-together metal armor", "a battered wasteland leather jacket", "a modified vault suit",
    "a grimy merchant's duster", "a military jacket over cargo pants", "Raider-style spiked leather armor",
    "a clean white lab coat", "a full-body hooded cloak",
]

NPC_MOTIVES = [
    "protect his little camp", "find his missing family", "save enough caps to buy a good gun",
    "avenge a friend", "find a legendary vault", "escape Raider pursuit",
    "build a safe settlement", "research pre-war technology",
    "locate a clean water source", "haul his goods to Diamond City",
]

NPC_KNOWLEDGE = [
    "there's an unlooted military depot nearby", "he knows a safe trail through the wastes",
    "he heard a faction has been making moves lately", "he spotted a rare mutant creature in the area",
    "he knows a hidden merchant outpost", "he knows the entrance to an abandoned vault",
    "he picked up a mysterious radio signal", "he once saw Synth activity somewhere",
    "he knows the local Raider patrol routes", "he knows the location of a water source",
]

NPC_SPEECH = [
    "direct and to the point", "chatty and loves gossip", "cautious and guarded, choosing every word",
    "blunt and crude", "polite and well-spoken, clearly educated", "speaks with a thick regional drawl",
    "mutters to himself constantly", "few words, but every one counts",
]

# ---------------------------------------------------------------------------
# Weather Table (12 types)
# ---------------------------------------------------------------------------

WEATHER_TABLE = [
    {"weather": "Clear", "desc": "A rare fine day in the wasteland. Bright sun, wide-open visibility.", "effect": "None", "weight": 20},
    {"weather": "Overcast", "desc": "Gray clouds obscure most of the sky, with occasional shafts of sunlight.", "effect": "None", "weight": 15},
    {"weather": "Cloudy", "desc": "Heavy cloud cover blankets the sky. The air is muggy and still.", "effect": "None", "weight": 15},
    {"weather": "Light Rain", "desc": "Sparse raindrops fall, carrying a faint chemical tang.", "effect": "Minor rads (+5/hr)", "weight": 10},
    {"weather": "Heavy Rain", "desc": "Driving rain reduces visibility. The water carries mild radiation.", "effect": "PER checks -1, +10 rads/hr", "weight": 5},
    {"weather": "Dusty", "desc": "Wind-whipped grit stings exposed skin. Breathing is difficult.", "effect": "PER checks -1", "weight": 8},
    {"weather": "Dust Storm", "desc": "A wall of yellow dust — total whiteout. Take shelter and wait.", "effect": "PER checks -3, movement impaired", "weight": 3},
    {"weather": "Dense Fog", "desc": "Thick fog smothers the wasteland. Can't see five paces ahead.", "effect": "PER checks -2, Sneak +2", "weight": 8},
    {"weather": "Rad Storm", "desc": "Green-tinged sky, lightning laced with radiation. Extremely dangerous.", "effect": "+50 rads/round, shelter required", "weight": 3},
    {"weather": "Scorching Heat", "desc": "Surface temperatures are brutal. Metal blisters to the touch.", "effect": "END check/hr or -5 HP", "weight": 5},
    {"weather": "Bitter Cold", "desc": "Temperature drops sharply. Breath turns to fog.", "effect": "END check/hr or -3 HP", "weight": 3},
    {"weather": "Light Breeze", "desc": "A cool breeze drifts across the wasteland, carrying the scent of distant fires.", "effect": "None", "weight": 5},
]
