package ui

import "testing"

func TestNextTab(t *testing.T) {
	if nextTab(navDashboard, navTests) != navTests {
		t.Fatalf("explicit select should win")
	}
	if tabLabel(navTests) != "Tests" || tabLabel(navEvents) != "Events" || tabLabel(navDashboard) != "Dashboard" {
		t.Fatalf("tab labels wrong")
	}
}

func TestMatrixCellStyle(t *testing.T) {
	if matrixCellColor(950) != colGood || matrixCellColor(600) != colWatch || matrixCellColor(100) != colBad {
		t.Fatalf("cell colors wrong")
	}
	if matrixCellText(-1) != "—" || matrixCellText(941) != "941" {
		t.Fatalf("cell text wrong")
	}
}
