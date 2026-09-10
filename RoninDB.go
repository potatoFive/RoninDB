package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

var db *sql.DB
var tinyworldZones map[string]bool

func main() {
	var err error
	db, err = sql.Open("sqlite", "ronin.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Optional but recommended: Enable WAL mode for better concurrent read performance
	_, _ = db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;")

	// Load Tinyworld zones from file once at startup
	initTinyworld()

	// Handlers
	http.HandleFunc("/", landingPage)
	http.HandleFunc("/ronin", ronin)

	// Mobiles
	http.HandleFunc("/ronin/Mobiles", roninMobiles)
	http.HandleFunc("/ronin/Mobiles/Detail", mobDetail)

	// Objects
	http.HandleFunc("/ronin/Objects", roninObjects)
	http.HandleFunc("/ronin/Objects/Detail", objectDetail)

	// Zones
	http.HandleFunc("/ronin/Zones", roninZones)
	http.HandleFunc("/ronin/Zones/Detail", zoneDetail)

	http.HandleFunc("/files", files)

	// Static Map Serving
	http.Handle("/map/", http.StripPrefix("/map/", http.FileServer(http.Dir("map"))))

	// This makes everything in the "files" folder available under the URL /files/download/
	http.Handle("/files/download/", http.StripPrefix("/files/download/", http.FileServer(http.Dir("files"))))

	fmt.Println("Server starting at http://localhost:8081")
	log.Fatal(http.ListenAndServe(":8081", nil))
}

const themeCSS = `
	:root {
		color-scheme: light;
		--bg:#f4f7f6;
		--surface:#ffffff;
		--surface-alt:#f8f9fa;
		--text:#333333;
		--muted:#70757a;
		--border:#dfe1e5;
		--row-border:#f1f3f4;
		--header-bg:#2c3e50;
		--header-text:#ffffff;
		--hover:#f1f1f1;
		--accent:#1a73e8;
		--danger:#c5221f;
		--success:#188038;
		--purple:#704085;
		--flags:#999999;
	}
	html[data-theme="dark"] {
		color-scheme: dark;
		--bg:#0f1419;
		--surface:#1a2129;
		--surface-alt:#222b35;
		--text:#e6e8ea;
		--muted:#9aa0a6;
		--border:#3a4450;
		--row-border:#2a333e;
		--header-bg:#33475c;
		--header-text:#ffffff;
		--hover:#242e39;
		--accent:#8ab4f8;
		--danger:#f28b82;
		--success:#81c995;
		--purple:#c58af9;
		--flags:#8a9096;
	}
	#themeToggle { position:fixed; top:16px; right:16px; z-index:1000; padding:8px 14px; border:1px solid var(--border); border-radius:4px; background:var(--surface); color:var(--text); font-size:0.85rem; cursor:pointer; }
	#themeToggle:hover { border-color:var(--accent); color:var(--accent); }`

func themeFromRequest(req *http.Request) string {
	if c, err := req.Cookie("theme"); err == nil && (c.Value == "dark" || c.Value == "light") {
		return c.Value
	}
	return "light"
}

func themeToggle(theme string) string {
	label := "Dark"
	if theme == "dark" {
		label = "Light"
	}
	return fmt.Sprintf(`<button id="themeToggle" onclick="toggleTheme()" title="Toggle dark mode">%s</button>
<script>
function toggleTheme() {
	var next = document.documentElement.getAttribute("data-theme") === "dark" ? "light" : "dark";
	document.documentElement.setAttribute("data-theme", next);
	document.cookie = "theme=" + next + "; path=/; max-age=31536000; SameSite=Lax";
	document.getElementById("themeToggle").textContent = next === "dark" ? "Light" : "Dark";
}
</script>`, label)
}

// --- Menu Handlers ---

func landingPage(w http.ResponseWriter, req *http.Request) {
	theme := themeFromRequest(req)
	fmt.Fprintf(w, `<html data-theme="%s"><head><style>%s</style></head><body>%s<a href="/ronin">Ronin mud data</a><br><a href="/files">Files</a></body></html>`, theme, themeCSS, themeToggle(theme))
}

func ronin(w http.ResponseWriter, req *http.Request) {
	theme := themeFromRequest(req)
	fmt.Fprintf(w, `<html data-theme="%s"><head><style>%s</style></head><body>%s<h1>Ronin Database</h1><a href="/ronin/Mobiles">Mobiles</a><br><a href="/ronin/Objects">Objects</a><br><a href="/ronin/Zones">Zones & Maps</a></body></html>`, theme, themeCSS, themeToggle(theme))
}

// --- Mobile Handlers ---

func roninMobiles(w http.ResponseWriter, req *http.Request) {
	search := req.URL.Query().Get("search")
	tinyworldChecked := req.URL.Query().Get("tinyworld") != "off"

	// Reordered SELECT to put zoneName first for left-side display
	query := `SELECT zoneName, mobNumber, mobName, mobKeywords, mobLevel, mobHPAffective, mobEXPPerEffectiveHP, mobCoins, mobCoinEXP, mobTotalExp FROM mobs`
	var args []interface{}
	hasWhere := false

	if search != "" {
		query += " WHERE mobName LIKE ? OR mobKeywords LIKE ?"
		args = append(args, "%"+search+"%", "%"+search+"%")
		hasWhere = true
	}

	if tinyworldChecked && len(tinyworldZones) > 0 {
		twZones := make([]string, 0, len(tinyworldZones))
		for z := range tinyworldZones {
			twZones = append(twZones, z)
		}
		sort.Strings(twZones)

		placeholders := make([]string, len(twZones))
		for i, z := range twZones {
			placeholders[i] = "?"
			args = append(args, z)
		}
		if hasWhere {
			query += " AND zoneName IN (" + strings.Join(placeholders, ",") + ")"
		} else {
			query += " WHERE zoneName IN (" + strings.Join(placeholders, ",") + ")"
		}
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	twChecked := ""
	if tinyworldChecked {
		twChecked = "checked"
	}
	theme := themeFromRequest(req)

	// Updated scan variables to match reordered SELECT
	var zone, id, name, keys, lvl, hp, exp, coins, coinExp, totalExp string

	fmt.Fprintf(w, `<html data-theme="%s"><head><style>%s
		body{font-family:sans-serif; padding:20px; background:var(--bg); color:var(--text);}
		table{border-collapse:collapse; width:100%%; background:var(--surface); box-shadow:0 2px 5px rgba(0,0,0,0.1);}
		th, td{border:1px solid var(--border); padding:12px; text-align:left;}
		th{background:var(--header-bg); color:var(--header-text); cursor:pointer; text-transform:uppercase; font-size:0.8rem;}
		tr:hover{background:var(--hover);}
		.search-box { margin-bottom: 20px; display: flex; gap: 10px; align-items: center; }
		input[type="text"] { padding: 8px; border: 1px solid var(--border); border-radius: 4px; width: 300px; background:var(--surface); color:var(--text); }
		.btn { padding: 8px 15px; background: #1a73e8; color: white; border: none; border-radius: 4px; cursor: pointer; text-decoration: none; }
		.back-link { display: inline-block; margin-bottom: 15px; color: var(--muted); text-decoration: none; }
		.checkbox-group { display: flex; align-items: center; gap: 5px; font-size: 0.8rem; }
	</style></head><body>%s
	<a href="/ronin" class="back-link">← Back to Menu</a>
	<h1>Mobiles</h1>

	<form method="GET" action="/ronin/Mobiles" class="search-box">
		<input type="text" name="search" placeholder="Search by name or keyword..." value="%s">
		<button type="submit" class="btn">Search</button>
		<a href="/ronin/Mobiles" class="btn" style="background:#6c757d;">Clear</a>
		<div class="checkbox-group">
			<input type="checkbox" name="tinyworld" id="tinyworld_mobile" %s>
			<label for="tinyworld_mobile" style="margin:0; text-transform:none;">Tinyworld</label>
		</div>
	</form>

	<table id="t"><thead><tr>
		<th onclick="sortTable(0)">Zone</th>
		<th onclick="sortTable(1)">Name</th>
		<th onclick="sortTable(2)">Keywords</th>
		<th onclick="sortTable(3)">Level</th>
		<th onclick="sortTable(4)">HP</th>
		<th onclick="sortTable(5)">Exp/HP</th>
		<th onclick="sortTable(6)">Coins</th>
		<th onclick="sortTable(7)">Coin Exp</th>
		<th onclick="sortTable(8)">Total Exp</th>
	</tr></thead><tbody>`, theme, themeCSS, themeToggle(theme), search, twChecked)

	if search != "" || tinyworldChecked {
		for rows.Next() {
			if err := rows.Scan(&zone, &id, &name, &keys, &lvl, &hp, &exp, &coins, &coinExp, &totalExp); err != nil {
				continue
			}
			fmt.Fprintf(w, "<tr><td>%s</td><td><a href='/ronin/Mobiles/Detail?id=%s'>%s</a></td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>", zone, id, name, keys, lvl, hp, exp, coins, coinExp, totalExp)
		}
	}
	fmt.Fprintf(w, `</tbody></table><script>
	function sortTable(n) {
		const table = document.getElementById("t");
		const tbody = table.tBodies[0];
		const rows = Array.from(tbody.rows);
		const dir = table.getAttribute("data-sort-dir") === "asc" ? "desc" : "asc";
		table.setAttribute("data-sort-dir", dir);

		rows.sort((a, b) => {
			let x = a.cells[n].innerText.trim();
			let y = b.cells[n].innerText.trim();
			const xNum = parseFloat(x.replace(/,/g, ''));
			const yNum = parseFloat(y.replace(/,/g, ''));
			if (!isNaN(xNum) && !isNaN(yNum)) {
				return dir === "asc" ? xNum - yNum : yNum - yNum;
			}
			return dir === "asc" ? x.localeCompare(y) : y.localeCompare(x);
		});

		rows.forEach(row => tbody.appendChild(row));
	}
	</script></body></html>`)
}

func mobDetail(w http.ResponseWriter, req *http.Request) {
	detailView(w, req, "mobs", "mobNumber", "Mobile")
}

// --- Object Handlers ---

func roninObjects(w http.ResponseWriter, req *http.Request) {
	affTypeRows, err := db.Query(`
		SELECT DISTINCT Affect0 FROM objects WHERE Affect0 IS NOT NULL AND Affect0 != '' AND Affect0 != '0'
		UNION
		SELECT DISTINCT Affect1 FROM objects WHERE Affect1 IS NOT NULL AND Affect1 != '' AND Affect1 != '0'
		UNION
		SELECT DISTINCT Affect2 FROM objects WHERE Affect2 IS NOT NULL AND Affect2 != '' AND Affect2 != '0'
		ORDER BY Affect0 ASC`)

	var allAffectTypes []string
	if err == nil {
		for affTypeRows.Next() {
			var t string
			if err := affTypeRows.Scan(&t); err == nil {
				allAffectTypes = append(allAffectTypes, t)
			}
		}
		affTypeRows.Close()
	}

	typeRows, _ := db.Query("SELECT DISTINCT itemType FROM objects WHERE itemType IS NOT NULL AND itemType != '' ORDER BY itemType ASC")
	var allTypes []string
	if typeRows != nil {
		for typeRows.Next() {
			var t string
			typeRows.Scan(&t)
			allTypes = append(allTypes, t)
		}
		typeRows.Close()
	}

	zoneRows, _ := db.Query("SELECT DISTINCT zone FROM objects WHERE zone IS NOT NULL AND zone != '' ORDER BY zone ASC")
	var allZones []string
	if zoneRows != nil {
		for zoneRows.Next() {
			var t string
			zoneRows.Scan(&t)
			allZones = append(allZones, t)
		}
		zoneRows.Close()
	}

	affFlagRows, _ := db.Query("SELECT DISTINCT objAffFlags FROM objects WHERE objAffFlags IS NOT NULL AND objAffFlags != ''")
	affFlagMap := make(map[string]bool)
	if affFlagRows != nil {
		for affFlagRows.Next() {
			var full string
			affFlagRows.Scan(&full)
			for _, f := range strings.Fields(full) {
				affFlagMap[f] = true
			}
		}
		affFlagRows.Close()
	}
	var allAffFlags []string
	for f := range affFlagMap {
		allAffFlags = append(allAffFlags, f)
	}
	sort.Strings(allAffFlags)

	search := req.URL.Query().Get("search")
	wear := req.URL.Query().Get("wear")
	itemType := req.URL.Query().Get("type")
	zone := req.URL.Query().Get("zone")
	affFlag := req.URL.Query().Get("affFlag")
	selAff := req.URL.Query().Get("affect")
	affectMod := req.URL.Query().Get("affectMod")
	extraFlag := req.URL.Query().Get("extraFlag")
	minDmg := req.URL.Query().Get("minDmg")
	alignment := req.URL.Query().Get("alignment")
	classReq := req.URL.Query().Get("class")
	excludeDecay := req.URL.Query().Get("excludeDecay") == "on"
	excludeAntiRent := req.URL.Query().Get("excludeAntiRent") == "on"
	tinyworldChecked := req.URL.Query().Get("tinyworld") == "on"
	excludeQuestwear := req.URL.Query().Get("excludeQuestwear") == "on"

	// Determine if we should show weapon-specific columns
	showWeaponInfo := itemType == "2H-WEAPON" || itemType == "WEAPON" || wear == "WIELD"

	var selectCols string
	if showWeaponInfo {
		selectCols = `itemNumber, shortDesc, wearFlags, damageAve, weaponType, weight, weaponSpecial, objAffFlags, Affect0, AffectModifier0, Affect1, AffectModifier1, Affect2, AffectModifier2, extraFlags, spell, zone`
	} else {
		selectCols = `itemNumber, shortDesc, wearFlags, affAC, damageAve, Affect0, AffectModifier0, Affect1, AffectModifier1, Affect2, AffectModifier2, extraFlags, spell, zone`
	}

	query := fmt.Sprintf("SELECT %s FROM objects WHERE 1=1", selectCols)
	var args []interface{}

	if search != "" {
		query += " AND (shortDesc LIKE ? OR keywords LIKE ?)"
		args = append(args, "%"+search+"%", "%"+search+"%")
	}
	if wear != "" && wear != "All" {
		query += " AND wearFlags LIKE ?"
		args = append(args, "%"+wear+"%")
	}
	if itemType != "" && itemType != "All" {
		query += " AND itemType = ?"
		args = append(args, itemType)
	}
	if zone != "" && zone != "All" {
		query += " AND zone = ?"
		args = append(args, zone)
	} else {
		//Hide 00Gear/Boards/Clan Halls items unless the zone dropdown explicitly selects them
		query += " AND zone != ? AND zone != ? AND zone != ?"
		args = append(args, "00Gear", "Boards", "Clan Halls")
	}
	if affFlag != "" && affFlag != "All" {
		query += " AND (objAffFlags = ? OR objAffFlags LIKE ? OR objAffFlags LIKE ? OR objAffFlags LIKE ?)"
		args = append(args, affFlag, affFlag+" %", "% "+affFlag, "% "+affFlag+" %")
	}

	if selAff != "" && selAff != "All" {
		if affectMod != "" {
			var modOp string
			var modVal int
			if val, err := strconv.Atoi(affectMod); err == nil {
				if val > 0 {
					modOp = ">="
					modVal = val
				} else if val < 0 {
					modOp = "<="
					modVal = val
				} else {
					modOp = "="
					modVal = 0
				}
				query += " AND (Affect0 = ? AND CAST(AffectModifier0 AS INTEGER) " + modOp + " ? OR Affect1 = ? AND CAST(AffectModifier1 AS INTEGER) " + modOp + " ? OR Affect2 = ? AND CAST(AffectModifier2 AS INTEGER) " + modOp + "?)"
				args = append(args, selAff, modVal, selAff, modVal, selAff, modVal)
			}
		} else {
			query += " AND (Affect0 = ? OR Affect1 = ? OR Affect2 = ?)"
			args = append(args, selAff, selAff, selAff)
		}
	}

	if extraFlag != "" && extraFlag != "All" {
		query += " AND extraFlags LIKE ?"
		args = append(args, "%"+extraFlag+"%")
	}

	if minDmg != "" {
		query += " AND CAST(damageAve AS INTEGER) >= ?"
		args = append(args, minDmg)
	}
	if excludeDecay {
		query += " AND extraFlags NOT LIKE '%DECAY%'"
	}
	if excludeAntiRent {
		query += " AND extraFlags NOT LIKE '%ANTI-RENT%'"
	}
	if excludeQuestwear {
		query += " AND wearFlags NOT LIKE '%QUESTWEAR%' AND zone NOT LIKE 'Quest Gear%'"
	}

	if alignment == "Good" {
		query += " AND extraFlags NOT LIKE '%ANTI-GOOD%'"
	} else if alignment == "Evil" {
		query += " AND extraFlags NOT LIKE '%ANTI-EVIL%'"
	} else if alignment == "All" {
		query += " AND extraFlags NOT LIKE '%ANTI-GOOD%' AND extraFlags NOT LIKE '%ANTI-EVIL%' AND extraFlags NOT LIKE '%ANTI-NEUTRAL%'"
	}

	classAntiTags := map[string]string{
		"Mage":         "%ANTI-MAGIC_USER%",
		"Warrior":      "%ANTI-WARRIOR%",
		"Thief":        "%ANTI-THIEF%",
		"Paladin":      "%ANTI-PALADIN%",
		"Nomad":        "%ANTI-NOMAD%",
		"Ninja":        "%ANTI-NINJA%",
		"Commando":     "%ANTI-COMMANDO%",
		"Cleric":       "%ANTI-CLERIC%",
		"Bard":         "%ANTI-BARD%",
		"Anti-Paladin": "%ANTI-ANTI-PALADIN%",
	}

	if classReq == "No-Anti" {
		for _, tag := range classAntiTags {
			query += " AND extraFlags NOT LIKE ?"
			args = append(args, tag)
		}
	} else if tag, ok := classAntiTags[classReq]; ok {
		query += " AND extraFlags NOT LIKE ?"
		args = append(args, tag)
	}

	if tinyworldChecked && len(tinyworldZones) > 0 {
		twZones := make([]string, 0, len(tinyworldZones))
		for z := range tinyworldZones {
			twZones = append(twZones, z)
		}
		sort.Strings(twZones)

		placeholders := make([]string, len(twZones))
		for i, z := range twZones {
			placeholders[i] = "?"
			args = append(args, z)
		}
		query += " AND zone IN (" + strings.Join(placeholders, ",") + ")"
	}

	if search == "" && wear == "" && (itemType == "" || itemType == "All") && (zone == "" || zone == "All") && (affFlag == "" || affFlag == "All") && (selAff == "" || selAff == "All") && affectMod == "" && extraFlag == "" && minDmg == "" && alignment == "" && (classReq == "" || classReq == "All") && !excludeDecay && !excludeAntiRent && !tinyworldChecked && !excludeQuestwear {
		query += " LIMIT 0"
	} else {
		query += " LIMIT 500"
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	decayChecked := ""
	if excludeDecay {
		decayChecked = "checked"
	}
	rentChecked := ""
	if excludeAntiRent {
		rentChecked = "checked"
	}
	twChecked := ""
	if tinyworldChecked {
		twChecked = "checked"
	}
	questwearChecked := ""
	if excludeQuestwear {
		questwearChecked = "checked"
	}
	theme := themeFromRequest(req)

	fmt.Fprintf(w, `
	<html data-theme="%s">
	<head>
		<title>Ronin Object Search</title>
		<style>%s
			body { font-family: -apple-system, sans-serif; background: var(--bg); color: var(--text); margin: 0; }
			.container { max-width: 1600px; margin: 0 auto; padding: 40px 20px; }
			.filter-panel { 
				background: var(--surface-alt); padding: 20px; border-radius: 8px; 
				display: flex; flex-wrap: wrap; gap: 15px; align-items: flex-end;
				margin-bottom: 30px; border: 1px solid var(--border);
			}
			.filter-group { display: flex; flex-direction: column; text-align: left; }
			.filter-group label { font-size: 0.7rem; font-weight: bold; color: var(--muted); margin-bottom: 5px; text-transform: uppercase; }
			.checkbox-group { display: flex; align-items: center; gap: 5px; font-size: 0.8rem; color: var(--muted); margin-bottom: 8px;}
			input, select { padding: 8px; border: 1px solid var(--border); border-radius: 4px; font-size: 0.9rem; background: var(--surface); color: var(--text); }
			.btn-search { background: #1a73e8; color: white; border: none; padding: 9px 20px; cursor: pointer; border-radius: 4px; font-weight: 500; }
			.back-link { color: var(--muted); text-decoration: none; font-size: 0.9rem; display: inline-block; margin-bottom: 20px; }
			
			table { width: 100%%; border-collapse: collapse; text-align: left; font-size: 0.85rem; }
			th { border-bottom: 2px solid var(--row-border); padding: 12px 8px; cursor: pointer; color: var(--muted); font-size: 0.65rem; text-transform: uppercase; }
			th:hover { background: var(--hover); color: var(--accent); }
			td { border-bottom: 1px solid var(--row-border); padding: 10px 8px; }
			tr:hover { background: var(--hover); }
			.dmg-val { color: var(--danger); font-weight: bold; }
			.aff-val { color: var(--success); font-weight: bold; }
			.spell-val { color: var(--purple); font-style: italic; }
			.flags-text { color: var(--flags); font-size: 0.7rem; }
			.item-link { color: var(--accent); text-decoration: none; font-weight: 500; }
		</style>
	</head>
	<body>%s
		<div class="container">
			<a href="/ronin" class="back-link">← Back to Menu</a>
			<h1>Object Search</h1>
			
			<form method="GET" action="/ronin/Objects" class="filter-panel">
				<div class="filter-group"><label>Keywords</label><input type="text" name="search" value="%s"></div>`, theme, themeCSS, themeToggle(theme), search)

	fmt.Fprintf(w, `<div class="filter-group"><label>Item Type</label><select name="type"><option>All</option>`)
	for _, t := range allTypes {
		s := ""
		if t == itemType {
			s = "selected"
		}
		fmt.Fprintf(w, `<option value="%s" %s>%s</option>`, t, s, t)
	}
	fmt.Fprintf(w, `</select></div>`)

	fmt.Fprintf(w, `<div class="filter-group"><label>Zone</label><select name="zone"><option>All</option>`)
	for _, z := range allZones {
		s := ""
		if z == zone {
			s = "selected"
		}
		fmt.Fprintf(w, `<option value="%s" %s>%s</option>`, z, s, z)
	}
	fmt.Fprintf(w, `</select></div>`)

	fmt.Fprintf(w, `<div class="filter-group"><label>Affect Flag</label><select name="affFlag"><option>All</option>`)
	for _, f := range allAffFlags {
		s := ""
		if f == affFlag {
			s = "selected"
		}
		fmt.Fprintf(w, `<option value="%s" %s>%s</option>`, f, s, f)
	}
	fmt.Fprintf(w, `</select></div>`)

	fmt.Fprintf(w, `<div class="filter-group"><label>Stat Affect</label><select name="affect"><option>All</option>`)
	for _, a := range allAffectTypes {
		s := ""
		if a == selAff {
			s = "selected"
		}
		fmt.Fprintf(w, `<option value="%s" %s>%s</option>`, a, s, a)
	}
	fmt.Fprintf(w, `</select></div>`)

	fmt.Fprintf(w, `<div class="filter-group"><label>Affect Modifier</label><select name="affectMod"><option value="">Any</option>`)
	for i := -10; i <= 10; i++ {
		s := ""
		if fmt.Sprintf("%d", i) == affectMod {
			s = "selected"
		}
		fmt.Fprintf(w, `<option value="%d" %s>%d</option>`, i, s, i)
	}
	fmt.Fprintf(w, `</select></div>`)

	fmt.Fprintf(w, `<div class="filter-group"><label>Extra Flags</label><select name="extraFlag"><option value="">All</option>`)
	extraFlags := []string{"HUM", "LIMITED", "GLOW", "DARK", "EVIL", "MAGICAL", "DISPELLED", "BLESSED"}
	for _, f := range extraFlags {
		s := ""
		if f == extraFlag {
			s = "selected"
		}
		fmt.Fprintf(w, `<option value="%s" %s>%s</option>`, f, s, f)
	}
	fmt.Fprintf(w, `</select></div>`)

	fmt.Fprintf(w, `<div class="filter-group"><label>Wear</label><select name="wear">
				<option>All</option>
				<option %s>TAKE</option><option %s>FINGER</option><option %s>NECK</option><option %s>BODY</option>
				<option %s>HEAD</option><option %s>LEGS</option><option %s>FEET</option><option %s>HANDS</option>
				<option %s>ARMS</option><option %s>SHIELD</option><option %s>ABOUT</option><option %s>WAIST</option>
				<option %s>WRIST</option><option %s>WIELD</option><option %s>HOLD</option>
			</select></div>`,
		sel(wear, "TAKE"), sel(wear, "FINGER"), sel(wear, "NECK"), sel(wear, "BODY"), sel(wear, "HEAD"), sel(wear, "LEGS"), sel(wear, "FEET"), sel(wear, "HANDS"), sel(wear, "ARMS"), sel(wear, "SHIELD"), sel(wear, "ABOUT"), sel(wear, "WAIST"), sel(wear, "WRIST"), sel(wear, "WIELD"), sel(wear, "HOLD"))

	fmt.Fprintf(w, `<div class="filter-group"><label>Class</label><select name="class">
			<option>All</option>
			<option %s>No-Anti</option>
			<option %s>Mage</option><option %s>Warrior</option><option %s>Thief</option>
			<option %s>Paladin</option><option %s>Nomad</option><option %s>Ninja</option>
			<option %s>Commando</option><option %s>Cleric</option><option %s>Bard</option>
			<option %s>Anti-Paladin</option>
		</select></div>`,
		sel(classReq, "No-Anti"), sel(classReq, "Mage"), sel(classReq, "Warrior"), sel(classReq, "Thief"), sel(classReq, "Paladin"), sel(classReq, "Nomad"), sel(classReq, "Ninja"), sel(classReq, "Commando"), sel(classReq, "Cleric"), sel(classReq, "Bard"), sel(classReq, "Anti-Paladin"))

	fmt.Fprintf(w, `<div class="filter-group"><label>Alignment</label><select name="alignment">
				<option value="">None</option>
				<option %s>All</option><option %s>Good</option><option %s>Evil</option>
			</select></div>`, sel(alignment, "All"), sel(alignment, "Good"), sel(alignment, "Evil"))

	fmt.Fprintf(w, `<div class="filter-group"><label>Min Dmg</label><input type="number" name="minDmg" value="%s" style="width:70px;"></div>`, minDmg)

	fmt.Fprintf(w, `
				<div class="filter-group">
					<div class="checkbox-group">
						<input type="checkbox" name="excludeDecay" id="decay" %s>
						<label for="decay" style="margin:0; text-transform:none;">Exclude Decay</label>
					</div>
					<div class="checkbox-group">
						<input type="checkbox" name="excludeAntiRent" id="antirent" %s>
						<label for="antirent" style="margin:0; text-transform:none;">Exclude AntiRent</label>
					</div>
					<div class="checkbox-group">
						<input type="checkbox" name="tinyworld" id="tinyworld" %s>
						<label for="tinyworld" style="margin:0; text-transform:none;">Tinyworld</label>
					</div>
					<div class="checkbox-group">
						<input type="checkbox" name="excludeQuestwear" id="questwear" %s>
						<label for="questwear" style="margin:0; text-transform:none;">Exclude Questwear</label>
					</div>
				</div>
				<button type="submit" class="btn-search">Search</button>
			</form>`, decayChecked, rentChecked, twChecked, questwearChecked)

	if search != "" || wear != "" || (itemType != "" && itemType != "All") || (zone != "" && zone != "All") || (affFlag != "" && affFlag != "All") || (selAff != "" && selAff != "All") || affectMod != "" || extraFlag != "" || minDmg != "" || alignment != "" || (classReq != "" && classReq != "All") || excludeDecay || excludeAntiRent || tinyworldChecked || excludeQuestwear {

		// Define display order based on filter state
		var colOrder []string
		if showWeaponInfo {
			colOrder = []string{"zone", "itemNumber", "shortDesc", "wearFlags", "damageAve", "weaponType", "weight", "weaponSpecial", "objAffFlags", "Affect0", "AffectModifier0", "Affect1", "AffectModifier1", "Affect2", "AffectModifier2", "extraFlags", "spell"}
		} else {
			colOrder = []string{"zone", "itemNumber", "shortDesc", "wearFlags", "affAC", "damageAve", "Affect0", "AffectModifier0", "Affect1", "AffectModifier1", "Affect2", "AffectModifier2", "extraFlags", "spell"}
		}

		headerHTML := "<tr>"
		for i, c := range colOrder {
			label := c
			switch c {
			case "itemNumber":
				label = "ID"
			case "shortDesc":
				label = "Short Description"
			case "wearFlags":
				label = "Wear"
			case "affAC":
				label = "AC"
			case "damageAve":
				label = "Avg Dmg"
			case "weaponType":
				label = "Type"
			case "weight":
				label = "Weight"
			case "weaponSpecial":
				label = "Special"
			case "objAffFlags":
				label = "AffFlags"
			case "spell":
				label = "Spell"
			}
			headerHTML += fmt.Sprintf(`<th onclick="sortTable(%d)">%s</th>`, i, label)
		}
		headerHTML += "</tr>"

		fmt.Fprintf(w, `
			<table id="sortableTable">
				<thead>%s</thead>
				<tbody>`, headerHTML)

		cols, _ := rows.Columns()
		for rows.Next() {
			values := make([]interface{}, len(cols))
			valuePtrs := make([]interface{}, len(cols))
			for i := range values {
				valuePtrs[i] = &values[i]
			}

			if err := rows.Scan(valuePtrs...); err != nil {
				continue
			}

			rowMap := make(map[string]string)
			for i, c := range cols {
				v := fmt.Sprintf("%v", values[i])
				if v == "<nil>" || v == "" {
					v = "-"
				}
				rowMap[c] = v
			}

			rowHTML := "<tr>"
			for _, c := range colOrder {
				val := rowMap[c]
				switch c {
				case "zone":
					rowHTML += fmt.Sprintf("<td>%s</td>", val)
				case "itemNumber":
					idStr := val
					nameStr := url.QueryEscape(rowMap["shortDesc"])
					rowHTML += fmt.Sprintf(`<td style="color:#bdc1c6;">%s</td>`, idStr)
					// Fixed: Added missing opening <td> tag for the Short Description link
					rowHTML += fmt.Sprintf(`<td><a class="item-link" href="/ronin/Objects/Detail?id=%s&name=%s">%s</a></td>`, idStr, nameStr, rowMap["shortDesc"])
				case "wearFlags":
					rowHTML += fmt.Sprintf(`<td style="color:#666; font-size:0.75rem;">%s</td>`, val)
				case "affAC":
					rowHTML += fmt.Sprintf("<td>%s</td>", val)
				case "damageAve":
					rowHTML += fmt.Sprintf(`<td class="dmg-val">%s</td>`, val)
				case "weaponType", "weight", "weaponSpecial":
					rowHTML += fmt.Sprintf("<td>%s</td>", val)
				case "objAffFlags":
					rowHTML += fmt.Sprintf(`<td class="flags-text">%s</td>`, val)
				case "spell":
					rowHTML += fmt.Sprintf(`<td class="spell-val">%s</td>`, val)
				case "Affect0", "Affect1", "Affect2":
					rowHTML += fmt.Sprintf("<td>%s</td>", val)
				case "AffectModifier0", "AffectModifier1", "AffectModifier2":
					rowHTML += fmt.Sprintf(`<td class="aff-val">%s</td>`, val)
				case "extraFlags":
					rowHTML += fmt.Sprintf(`<td class="flags-text">%s</td>`, val)
				}
			}
			rowHTML += "</tr>"
			fmt.Fprint(w, rowHTML)
		}
		fmt.Fprintf(w, `</tbody></table>
		<script>
		function sortTable(n) {
			const table = document.getElementById("sortableTable");
			const rows = Array.from(table.rows).slice(1);
			const dir = table.getAttribute("data-dir") === "asc" ? "desc" : "asc";
			table.setAttribute("data-dir", dir);
			rows.sort((a, b) => {
				let x = a.cells[n].innerText.trim();
				let y = b.cells[n].innerText.trim();
				let xNum = parseFloat(x === "-" ? "0" : x);
				let yNum = parseFloat(y === "-" ? "0" : y);
				if (!isNaN(xNum) && !isNaN(yNum)) return dir === "asc" ? xNum - yNum : yNum - yNum;
				return dir === "asc" ? x.localeCompare(y) : y.localeCompare(x);
			});
			rows.forEach(row => table.tBodies[0].appendChild(row));
		}
		</script>`)
	}
	fmt.Fprintf(w, "</div></body></html>")
}

func sel(current, target string) string {
	if current == target {
		return "selected"
	}
	return ""
}

func objectDetail(w http.ResponseWriter, req *http.Request) {
	id := req.URL.Query().Get("id")
	name := req.URL.Query().Get("name")

	rows, err := db.Query("SELECT * FROM objects WHERE itemNumber = ? AND shortDesc = ? LIMIT 1", id, name)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	cols, _ := rows.Columns()

	theme := themeFromRequest(req)
	fmt.Fprintf(w, `<html data-theme="%s"><head><style>%s
		body { font-family: sans-serif; padding: 20px; background: var(--bg); color: var(--text); }
		.detail-card { background: var(--surface); padding: 20px; border-radius: 8px; box-shadow: 0 2px 10px rgba(0,0,0,0.1); max-width: 800px; margin: auto; }
		table { width: 100%%; border-collapse: collapse; margin-top: 20px; }
		th, td { text-align: left; padding: 12px; border-bottom: 1px solid var(--row-border); }
		th { background: var(--surface-alt); color: var(--muted); width: 30%%; }
		.back-btn { text-decoration: none; color: var(--accent); font-weight: bold; }
		.item-ref { color: var(--accent); text-decoration: none; font-weight: bold; border-bottom: 1px dashed var(--accent); }
	</style></head><body>%s
	<div class="detail-card">
		<a href="javascript:history.back()" class="back-btn">← Back</a>
		<h1>Object Detail: %s</h1>
		<table>`, theme, themeCSS, themeToggle(theme), id)

	if rows.Next() {
		values := make([]interface{}, len(cols))
		valuePtrs := make([]interface{}, len(cols))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		for i, colName := range cols {
			var valStr string
			val := values[i]

			switch v := val.(type) {
			case []byte:
				valStr = string(v)
			case nil:
				valStr = "-"
			default:
				valStr = fmt.Sprintf("%v", v)
			}

			if valStr == "" || valStr == "0" {
				valStr = "-"
			}

			targetCols := map[string]bool{
				"recipeRequires1": true,
				"recipeRequires2": true,
				"recipeRequires3": true,
				"recipeCreates":   true,
			}

			if valStr != "-" && targetCols[colName] {
				ids := strings.Fields(valStr)
				var links []string

				for _, itemID := range ids {
					var shortDesc string
					err := db.QueryRow("SELECT shortDesc FROM objects WHERE itemNumber = ?", itemID).Scan(&shortDesc)
					if err == nil {
						links = append(links, fmt.Sprintf("<a class='item-ref' href='/ronin/Objects/Detail?id=%s&name=%s'>%s</a> [%s]", itemID, url.QueryEscape(shortDesc), shortDesc, itemID))
					} else {
						links = append(links, itemID)
					}
				}
				valStr = strings.Join(links, ", ")
			}

			fmt.Fprintf(w, "<tr><th>%s</th><td>%s</td></tr>", colName, valStr)
		}

		//Mobs that load this item (zone reset G/E lines reference the last M/F/R mobile)
		loaderRows, lerr := db.Query(`SELECT DISTINCT z.ZoneName, m.mobShortDesc, m.mobNumber FROM zones z JOIN mobs m ON m.zoneNumber = z.ZoneNumber AND m.mobNumber = z.spawnItemMobID WHERE z.spawnItemID = ? AND z.spawnItemType IN ('ObjectToMob', 'ObjectToMobEQ')`, id)
		if lerr == nil {
			var loaderLinks []string
			for loaderRows.Next() {
				var zoneName, mobShortDesc, mobNumber string
				if loaderRows.Scan(&zoneName, &mobShortDesc, &mobNumber) != nil {
					continue
				}
				loaderLinks = append(loaderLinks, fmt.Sprintf("<a class='item-ref' href='/ronin/Mobiles/Detail?id=%s'>%s</a> [%s]", mobNumber, strings.TrimSpace(mobShortDesc), zoneName))
			}
			loaderRows.Close()
			if len(loaderLinks) > 0 {
				fmt.Fprintf(w, "<tr><th>Loaded By</th><td>%s</td></tr>", strings.Join(loaderLinks, ", "))
			}
		}
	} else {
		fmt.Fprintf(w, "<tr><td colspan='2'>No record found for ID %s</td></tr>", id)
	}

	fmt.Fprintf(w, `</table></div></body></html>`)
}

// --- Zone Handlers ---

func roninZones(w http.ResponseWriter, req *http.Request) {
	// Tinyworld filter completely removed as requested

	query := `SELECT ZoneNumber, ZoneName, MAX(spawnMobileRoomID) FROM zones WHERE 1=1 GROUP BY ZoneNumber`
	var args []interface{}

	rows, err := db.Query(query, args...)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	theme := themeFromRequest(req)
	fmt.Fprintf(w, `
	<html data-theme="%s">
	<head>
		<style>%s
			body { font-family: sans-serif; padding: 20px; background: var(--bg); color: var(--text); }
			table { border-collapse: collapse; width: 100%%; background: var(--surface); box-shadow: 0 2px 5px rgba(0,0,0,0.1); }
			th, td { border: 1px solid var(--border); padding: 12px; text-align: left; }
			th { background: var(--header-bg); color: var(--header-text); cursor: pointer; text-transform:uppercase; font-size: 0.8rem; }
			tr:hover { background: var(--hover); }
			.back-link { display: inline-block; margin-bottom: 15px; color: var(--muted); text-decoration: none; }
		</style>
	</head>
	<body>%s
		<a href="/ronin" class="back-link">← Back to Menu</a>
		<h1>Zones</h1>

		<table id="zonesTable">
			<thead>
				<tr>
					<th onclick="sortTable(0)">ID</th>
					<th onclick="sortTable(1)">Name</th>
					<th onclick="sortTable(2)">Last Room</th>
					<th onclick="sortTable(3)">Map</th>
				</tr>
			</thead>
			<tbody>`, theme, themeCSS, themeToggle(theme))

	for rows.Next() {
		var id, name, room string
		rows.Scan(&id, &name, &room)
		mapFile := fmt.Sprintf("%s-%s.png", id, name)
		mapLink := "No Map"
		if _, err := os.Stat(filepath.Join("map", mapFile)); err == nil {
			mapLink = fmt.Sprintf("<a href='/map/%s' target='_blank'>View</a>", mapFile)
		}
		fmt.Fprintf(w, "<tr><td>%s</td><td><a href='/ronin/Zones/Detail?id=%s'>%s</a></td><td>%s</td><td>%s</td></tr>", id, id, name, room, mapLink)
	}

	fmt.Fprintf(w, `</tbody></table>
	<script>
	function sortTable(n) {
		const table = document.getElementById("zonesTable");
		const tbody = table.tBodies[0];
		const rows = Array.from(tbody.rows);
		const dir = table.getAttribute("data-sort-dir") === "asc" ? "desc" : "asc";
		table.setAttribute("data-sort-dir", dir);

		rows.sort((a, b) => {
			let x = a.cells[n].innerText.trim();
			let y = b.cells[n].innerText.trim();
			const xNum = parseFloat(x.replace(/,/g, ''));
			const yNum = parseFloat(y.replace(/,/g, ''));
			if (!isNaN(xNum) && !isNaN(yNum)) {
				return dir === "asc" ? xNum - yNum : yNum - yNum;
			}
			return dir === "asc" ? x.localeCompare(y) : y.localeCompare(x);
		});

		rows.forEach(row => tbody.appendChild(row));
	}
	</script>
	</body></html>`)
}

func zoneDetail(w http.ResponseWriter, req *http.Request) {
	zoneID := req.URL.Query().Get("id")
	rows, err := db.Query("SELECT * FROM zones WHERE ZoneNumber = ?", zoneID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	var mobIDs, itemIDs, roomIDs []string
	var zoneName string
	cols, _ := rows.Columns()
	general := make(map[string]interface{})

	for rows.Next() {
		ptrs := make([]interface{}, len(cols))
		vals := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		rows.Scan(ptrs...)

		if len(general) == 0 {
			for i, c := range cols {
				v := vals[i]
				if b, ok := v.([]byte); ok {
					general[c] = string(b)
				} else {
					general[c] = v
				}
			}
			zoneName = fmt.Sprintf("%v", general["ZoneName"])
		}

		for i, c := range cols {
			v := fmt.Sprintf("%v", vals[i])
			if v == "" || v == "<nil>" || v == "0" {
				continue
			}
			if c == "spawnMobileID" {
				mobIDs = append(mobIDs, v)
			}
			if c == "spawnItemID" {
				itemIDs = append(itemIDs, v)
			}
			if c == "spawnMobileRoomID" {
				roomIDs = append(roomIDs, v)
			}
		}
	}

	theme := themeFromRequest(req)
	fmt.Fprintf(w, `<html data-theme="%s"><head><style>%s
		body{font-family:sans-serif; padding:20px; background:var(--bg); color:var(--text);}
		.card{background:var(--surface); padding:20px; border-radius:8px; margin-bottom:20px; box-shadow:0 2px 5px rgba(0,0,0,0.1);}
		.data-list{background:var(--surface-alt); padding:10px; border-radius:5px; margin:10px 0; word-wrap:break-word;}
		.label{font-weight:bold; width:180px; display:inline-block;}
	</style></head><body>%s`, theme, themeCSS, themeToggle(theme))

	fmt.Fprintf(w, "<h1>Zone %s: %s</h1><a href='/ronin/Zones'>Back</a>", zoneID, zoneName)

	fmt.Fprintf(w, "<div class='card'><h2>Details</h2>")
	for _, c := range cols {
		fmt.Fprintf(w, "<div><span class='label'>%s:</span> %v</div>", c, general[c])
	}
	fmt.Fprintf(w, "</div>")

	fmt.Fprintf(w, "<div class='card'><h2>Spawns</h2>")

	fmt.Fprintf(w, "<strong>Mobiles:</strong><div class='data-list'>")
	uMobs := unique(mobIDs)
	for i, id := range uMobs {
		fmt.Fprintf(w, "<a href='/ronin/Mobiles/Detail?id=%s'>%s</a>", id, id)
		if i < len(uMobs)-1 {
			fmt.Fprintf(w, ", ")
		}
	}
	fmt.Fprintf(w, "</div>")

	fmt.Fprintf(w, "<strong>Items:</strong><div class='data-list'>")
	uItems := unique(itemIDs)
	for _, id := range uItems {
		var shortDesc string
		db.QueryRow("SELECT shortDesc FROM objects WHERE itemNumber = ? LIMIT 1", id).Scan(&shortDesc)
		nameEncoded := url.QueryEscape(shortDesc)
		fmt.Fprintf(w, "<a href='/ronin/Objects/Detail?id=%s&name=%s'>%s</a> [%s]", id, nameEncoded, shortDesc, id)
	}
	fmt.Fprintf(w, "</div>")

	fmt.Fprintf(w, "<strong>Rooms:</strong><div class='data-list'>%s</div>", strings.Join(unique(roomIDs), ", "))
	fmt.Fprintf(w, "</div></body></html>")
}

// --- Helpers ---

func detailView(w http.ResponseWriter, req *http.Request, table, idCol, title string) {
	id := req.URL.Query().Get("id")

	rows, err := db.Query(fmt.Sprintf("SELECT * FROM %s WHERE %s = ? LIMIT 1", table, idCol), id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	cols, _ := rows.Columns()

	theme := themeFromRequest(req)
	fmt.Fprintf(w, `<html data-theme="%s"><head><style>%s
		body { font-family: sans-serif; padding: 20px; background: var(--bg); color: var(--text); }
		.detail-card { background: var(--surface); padding: 20px; border-radius: 8px; box-shadow: 0 2px 10px rgba(0,0,0,0.1); max-width: 800px; margin: auto; }
		table { width: 100%%; border-collapse: collapse; margin-top: 20px; }
		th, td { text-align: left; padding: 12px; border-bottom: 1px solid var(--row-border); }
		th { background: var(--surface-alt); color: var(--muted); width: 30%%; }
		.back-btn { text-decoration: none; color: var(--accent); font-weight: bold; }
	</style></head><body>%s
	<div class="detail-card">
		<a href="javascript:history.back()" class="back-btn">← Back</a>
		<h1>%s Detail: %s</h1>
		<table>`, theme, themeCSS, themeToggle(theme), title, id)

	if rows.Next() {
		values := make([]interface{}, len(cols))
		valuePtrs := make([]interface{}, len(cols))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		for i, colName := range cols {
			var valStr string
			val := values[i]

			switch v := val.(type) {
			case []byte:
				valStr = string(v)
			case nil:
				valStr = "<span style='color:#ccc;'>NULL</span>"
			default:
				valStr = fmt.Sprintf("%v", v)
			}

			if valStr == "" || valStr == "0" {
				valStr = "-"
			}

			fmt.Fprintf(w, "<tr><th>%s</th><td>%s</td></tr>", colName, valStr)
		}
	} else {
		fmt.Fprintf(w, "<tr><td colspan='2'>No record found for ID %s</td></tr>", id)
	}

	fmt.Fprintf(w, `</table></div></body></html>`)
}

func unique(s []string) []string {
	m := make(map[string]bool)
	var res []string
	for _, v := range s {
		if !m[v] {
			m[v] = true
			res = append(res, v)
		}
	}
	return res
}

func files(w http.ResponseWriter, req *http.Request) {
	dirname := "files"

	entries, err := os.ReadDir(dirname)
	if err != nil {
		http.Error(w, "Unable to read directory", http.StatusInternalServerError)
		return
	}

	theme := themeFromRequest(req)
	fmt.Fprintf(w, `
	<html data-theme="%s">
	<head>
		<title>File Index</title>
		<style>%s
			body { font-family: -apple-system, sans-serif; background: var(--bg); color: var(--text); margin: 0; }
			.container { max-width: 800px; margin: 0 auto; background: var(--surface); padding: 20px; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
			h1 { font-weight: 300; border-bottom: 2px solid var(--row-border); padding-bottom: 10px; }
			table { width: 100%%; border-collapse: collapse; margin-top: 20px; }
			th, td { text-align: left; padding: 12px; border-bottom: 1px solid var(--row-border); }
			th { background: var(--surface-alt); color: var(--muted); font-size: 0.8rem; text-transform: uppercase; }
			.file-link { color: var(--accent); text-decoration: none; }
			.file-link:hover { text-decoration: underline; }
		</style>
	</head>
	<body>%s
		<div class="container">
			<a href="/ronin" style="color: #70757a; text-decoration: none; font-size: 0.9rem;">← Back to Ronin DB</a>
			<h1>Downloads</h1>
			<table>
				<thead>
					<tr>
						<th>Filename</th>
						<th>Size</th>
						<th>Action</th>
					</tr>
				</thead>
			<tbody>`, theme, themeCSS, themeToggle(theme))

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		info, _ := entry.Info()
		fileName := entry.Name()
		fileSize := fmt.Sprintf("%.2f KB", float64(info.Size())/1024)

		fmt.Fprintf(w, `
			<tr>
				<td>%s</td>
				<td>%s</td>
				<td><a class="file-link" href="/files/download/%s" download>Download</a></td>
			</tr>`, fileName, fileSize, fileName)
	}

	fmt.Fprintf(w, `
				</tbody>
			</table>
		</div>
	</body>
	</html>`)
}

// initTinyworld reads the tinyworld.idx file once at startup and caches the zone names.
func initTinyworld() {
	data, err := os.ReadFile("tinyworld.idx")
	if err != nil {
		log.Printf("Warning: Could not read tinyworld.idx: %v. Tinyworld filter will be inactive.", err)
		return
	}
	tinyworldZones = make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		zone := strings.TrimSpace(line)
		if zone != "" {
			tinyworldZones[zone] = true
		}
	}
	log.Printf("Loaded %d zones from tinyworld.idx", len(tinyworldZones))
}
