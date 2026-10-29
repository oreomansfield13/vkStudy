package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type Door struct {
	Name      string
	To        string
	OpenedBy  string
	IsOpen    bool
	LockedMsg string
	OpenedMsg string
}

func (d *Door) Use(item string) (string, bool) {
	if d.OpenedBy != item {
		return "", false
	}
	d.IsOpen = true
	return d.OpenedMsg, true
}

type Room struct {
	Name      string
	Items     []string
	Wearable  map[string]bool
	Neighbors map[string]*Room
	exitOrder []string
	Look      func() string
	Enter     func() string
	Doors     []*Door
}

func (r *Room) DoorByName(name string) *Door {
	for _, d := range r.Doors {
		if d.Name == name {
			return d
		}
	}
	return nil
}

func (r *Room) DoorTo(dest string) *Door {
	for _, d := range r.Doors {
		if d.To == dest {
			return d
		}
	}
	return nil
}

func (r *Room) CanWear(item string) bool {
	return r.Wearable[item]
}

type Player struct {
	Room      *Room
	Inventory []string
	worn      map[string]bool
}

func (p *Player) Wears(item string) bool {
	return p.worn[item]
}

type Game struct {
	Player *Player
}

var game *Game

func connect(a, b *Room) {
	a.Neighbors[b.Name] = b
	b.Neighbors[a.Name] = a
}

func newRoom(name string, exits []string, items ...string) *Room {
	return &Room{
		Name:      name,
		Items:     items,
		Neighbors: make(map[string]*Room, len(exits)),
		exitOrder: exits,
		Wearable:  map[string]bool{},
	}
}

func initGame() {
	kitchen := newRoom("кухня", []string{"коридор"}, "чай")
	corridor := newRoom("коридор", []string{"кухня", "комната", "улица"})
	livingRoom := newRoom("комната", []string{"коридор"}, "ключи", "конспекты", "рюкзак")
	street := newRoom("улица", []string{"домой"})

	livingRoom.Wearable["рюкзак"] = true

	connect(kitchen, corridor)
	connect(corridor, livingRoom)
	connect(corridor, street)

	game = &Game{
		Player: &Player{
			Room:      kitchen,
			Inventory: []string{},
			worn:      map[string]bool{},
		},
	}

	corridor.Doors = []*Door{
		{
			Name:      "дверь",
			To:        "улица",
			OpenedBy:  "ключи",
			LockedMsg: "дверь закрыта",
			OpenedMsg: "дверь открыта",
		},
	}

	kitchen.Look = func() string {
		s := "ты находишься на кухне"
		if len(kitchen.Items) > 0 {
			s += ", на столе: " + strings.Join(kitchen.Items, ", ")
		}
		if game.Player.Wears("рюкзак") {
			s += ", надо идти в универ"
		} else {
			s += ", надо собрать рюкзак и идти в универ"
		}
		return s + ". можно пройти - " + strings.Join(kitchen.exitOrder, ", ")
	}
	kitchen.Enter = func() string {
		return "кухня, ничего интересного. можно пройти - " + strings.Join(kitchen.exitOrder, ", ")
	}

	corridor.Look = func() string {
		return "ничего интересного. можно пройти - " + strings.Join(corridor.exitOrder, ", ")
	}
	corridor.Enter = corridor.Look

	livingRoom.Look = func() string {
		var tableItems, chairItems []string
		for _, it := range livingRoom.Items {
			if it == "рюкзак" {
				chairItems = append(chairItems, it)
			} else {
				tableItems = append(tableItems, it)
			}
		}
		var parts []string
		if len(tableItems) > 0 {
			parts = append(parts, "на столе: "+strings.Join(tableItems, ", "))
		}
		if len(chairItems) > 0 {
			parts = append(parts, "на стуле: "+strings.Join(chairItems, ", "))
		}
		desc := "пустая комната"
		if len(parts) > 0 {
			desc = strings.Join(parts, ", ")
		}
		return desc + ". можно пройти - " + strings.Join(livingRoom.exitOrder, ", ")
	}
	livingRoom.Enter = func() string {
		return "ты в своей комнате. можно пройти - " + strings.Join(livingRoom.exitOrder, ", ")
	}

	street.Look = func() string {
		return "на улице весна. можно пройти - " + strings.Join(street.exitOrder, ", ")
	}
	street.Enter = street.Look
}

func handleCommand(command string) string {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "неизвестная команда"
	}

	switch parts[0] {
	case "осмотреться":
		return lookAround()
	case "идти":
		if len(parts) < 2 {
			return "неизвестная команда"
		}
		return goTo(parts[1])
	case "надеть":
		if len(parts) < 2 {
			return "неизвестная команда"
		}
		return putOn(parts[1])
	case "взять":
		if len(parts) < 2 {
			return "неизвестная команда"
		}
		return take(parts[1])
	case "применить":
		if len(parts) < 3 {
			return "неизвестная команда"
		}
		return apply(parts[1], parts[2])
	default:
		return "неизвестная команда"
	}
}

func lookAround() string {
	return game.Player.Room.Look()
}

func goTo(destination string) string {
	p := game.Player
	next, ok := p.Room.Neighbors[destination]
	if !ok {
		return "нет пути в " + destination
	}
	if d := p.Room.DoorTo(destination); d != nil && !d.IsOpen {
		return d.LockedMsg
	}
	p.Room = next
	return next.Enter()
}

func putOn(item string) string {
	p := game.Player
	if !p.Room.CanWear(item) {
		return "нечего надеть"
	}
	if p.Wears(item) {
		return item + " уже надет"
	}
	if !removeItem(p.Room, item) {
		return "нет такого"
	}
	p.worn[item] = true
	return "вы надели: " + item
}

func take(item string) string {
	p := game.Player
	if !p.Wears("рюкзак") {
		return "некуда класть"
	}
	if !removeItem(p.Room, item) {
		return "нет такого"
	}
	p.Inventory = append(p.Inventory, item)
	return "предмет добавлен в инвентарь: " + item
}

func apply(item, target string) string {
	p := game.Player
	if !hasItem(p.Inventory, item) {
		return "нет предмета в инвентаре - " + item
	}
	d := p.Room.DoorByName(target)
	if d == nil {
		return "не к чему применить"
	}
	if msg, ok := d.Use(item); ok {
		return msg
	}
	return "не к чему применить"
}

func removeItem(r *Room, name string) bool {
	for i, it := range r.Items {
		if it == name {
			r.Items = append(r.Items[:i], r.Items[i+1:]...)
			return true
		}
	}
	return false
}

func hasItem(items []string, name string) bool {
	for _, it := range items {
		if it == name {
			return true
		}
	}
	return false
}

func main() {
	initGame()
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("Начало игры. Введите команду (Ctrl+D - выход):")
	for {
		fmt.Print("> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fmt.Println(handleCommand(line))
	}
}
