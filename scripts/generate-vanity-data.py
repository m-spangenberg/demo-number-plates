import requests

"""
Makes use of https://github.com/dwyl/english-words to source all english words with 7 or fewer characters 
"""

def download(url):
    response = requests.get(url)
    if response.status_code == 200:
        return response.text.splitlines()
    else:
        raise Exception(f"Failed to download data from {url}")
    
def save_to_file(data, filename):
    with open(filename, "w") as f:
        for plate in data:
            f.write(f"{plate}\n")

def main():

    generated_plates = set()

    dl_words = download("https://github.com/dwyl/english-words/raw/refs/heads/master/words.txt")

    words = [word for word in dl_words if len(word) <= 7]

    for word in words:
        generated_plates.add(word.lower())
        
        # TODO: I disabled this because it generates 13,241,931 vanity plates

        # plate = word.replace('E', '3').replace('A', '4').replace('S', '5')
        # generated_plates.add(plate)
        
        # for i in range(100):
        #     if len(plate) <= 7:
        #         generated_plates.add(f"{plate}{i}")

    save_to_file(generated_plates, "../data/vanity_plates.txt")

    print(f"Generated {len(generated_plates)} vanity plates.")


if __name__ == "__main__":
    main()
