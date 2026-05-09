from random import randint

STANDARD_PLATE_COUNT = 3_000_000

codes=[
    "A",
    "AR",
    "B",
    "BK",
    "DV",
    "EN",
    "HB",
    "HN",
    "KA",
    "KH",
    "KM",
    "KO",
    "KR",
    "MA",
    "MT",
    "NK",
    "OA",
    "OH",
    "OJ",
    "OK",
    "OM",
    "ON",
    "OP",
    "OR",
    "OV",
    "RC",
    "TK",
    "U",
    "UP",
    "W",
    "S",
    "WB",
    "GRN",
    "NDF",
    "POL",
    "NCS",
    "JUD"
]

def save_to_file(data, filename):
    with open(filename, "w") as f:
        for plate in data:
            f.write(f"{plate}\n")

def main():

    generated_plates = set()

    for i in range(STANDARD_PLATE_COUNT):
        random_number = randint(1, 999999)
        random_code = codes[randint(0, len(codes)-1)]

        generated_plates.add(f"{random_number}{random_code}")
        
    save_to_file(generated_plates, "../data/standard_plates.txt")

    print(f"Generated {len(generated_plates)} standard plates.")


if __name__ == "__main__":
    main()
