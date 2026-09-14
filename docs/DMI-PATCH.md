# DMI-Eintrag ergänzen

Der Treiber matcht mit `DMI_EXACT_MATCH`, also zeichengenau. Stimmen die
BIOS-Strings des N5 Pro nicht exakt mit `"N5 PRO"` / `"F8NAA"` überein,
scheitert `n5_probe()` mit `-ENODEV`.

## 1. Ist-Zustand feststellen

```
cat /sys/class/dmi/id/product_name
cat /sys/class/dmi/id/board_name
```

Die eckigen Klammern in der Ausgabe von `01-baseline.sh` zeigen führende oder
nachgestellte Leerzeichen — die sind für `DMI_EXACT_MATCH` relevant.

## 2. Eintrag ergänzen

In `build/minisforum_n5_it5571.c`, in `n5_dmi_table[]` vor dem
abschließenden `{ }`:

```c
	{
		.ident = "Minisforum N5 Pro / F8NAA (lokal, experimentell)",
		.matches = {
			DMI_EXACT_MATCH(DMI_PRODUCT_NAME, "<exakter product_name>"),
			DMI_EXACT_MATCH(DMI_BOARD_NAME,   "<exakter board_name>"),
		},
		.driver_data = &n5_pro_f8naa,
	},
```

`&n5_pro_f8naa` hat `.validated = false` — genau richtig. Damit bleibt das
Profil read-only, bis `experimental_write=1` gesetzt wird. Niemals auf
`&n5_f8naa` zeigen lassen, das würde PWM-Writes sofort freischalten.

## 3. Alternative ohne Patch

```
insmod build/minisforum_n5_it5571.ko force=1
```

`force` lädt ohne DMI-Match, aber ohne `experimental_write` ausschließlich
lesend. Für Phase 3–5 völlig ausreichend. Für Phase 6 lieber den sauberen
DMI-Eintrag verwenden, damit nicht versehentlich auf fremder Hardware
geschrieben wird.

## 4. Neu bauen

```
make -C build KDIR=/lib/modules/$(uname -r)/build
```

Der Patch liegt bewusst in `build/`, nicht in `upstream/` — `02-build-tools.sh`
macht auf `upstream/` ein `git reset --hard` und würde die Änderung verwerfen.
Änderungen, die du dauerhaft behalten willst, als Patchdatei unter `docs/`
ablegen.
