package generator_test

import (
	"fmt"

	"scss-bem-gen/internal/core"
	"scss-bem-gen/internal/generator"
)

// This example replaces the old demo in the repository's root main.go.
func Example() {
	src := `
<div className="page">
  <header className="page__header page__header--sticky">
    <nav className="page__header__nav">
      <ul className="page__header__nav__list">
        <li className="page__header__nav__list__nav-item page__header__nav__list__nav-item--active">
          <a href="#" className="page__header__nav__list__nav-item__nav-link">Home</a>
        </li>
        <li className="page__header__nav__list__nav-item">
          <a href="#" className="page__header__nav__list__nav-item__nav-link">About</a>
        </li>
      </ul>
    </nav>
  </header>
</div>`

	root, err := core.ParseString("example.jsx", src)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Print(generator.Generate(root, generator.Options{}))
	// Output:
	// .page {
	//   &__header {
	//     &--sticky {}
	//     &__nav {
	//       &__list {
	//         &__nav-item {
	//           &--active {}
	//           &__nav-link {}
	//         }
	//       }
	//     }
	//   }
	// }
}

func ExampleNestingBEM() {
	src := `<article className="card"><div className="card__body"><h2 className="card__title">Hi</h2></div></article>`
	root, _ := core.ParseString("card.jsx", src)
	fmt.Print(generator.Generate(root, generator.Options{Nesting: generator.NestingBEM}))
	// Output:
	// .card {
	//   &__body {}
	//   &__title {}
	// }
}
