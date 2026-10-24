// NoKeyOut · 板面坐标系的唯一出处（game 运行时使用）
//
// 板面坐标：原点 = 板左上角，x 向右、y 向下，单位 mm，板 48×48mm。
//   * ORIGIN_MM：板左上角在 KiCad 绝对坐标里的位置
//   * holes    ：四角安装孔中心与内外半径（板面 mm）
window.NK_BOARD_FRAME = {
  ORIGIN_MM: 100,
  BOARD_MM: 48,
  holes: {
    centers: [[2.85, 2.91], [45.06, 2.84], [2.74, 45.06], [45.05, 45.14]],
    rIn: 1.5, rOut: 3.15,
  },
};

// 屏幕模块实物排针（FBX 正面丝印，也是 3D pin1..8 的物理顺序）：
// GND VCC SCL SDA RES DC CS BLK；CLK/MOSI/RST 是同一根脚的功能名。
window.NK_SCREEN_PINS = {
  phys: ['GND', 'VCC', 'SCL', 'SDA', 'RES', 'DC', 'CS', 'BLK'],
  func: { VCC: 2, GND: 1, BLK: 8, CLK: 3, MOSI: 4, CS: 7, DC: 6, RST: 5 },
};
