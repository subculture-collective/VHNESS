import unittest

from land_release_receipts import core_revision

A, B, C = 'a' * 40, 'b' * 40, 'c' * 40


def labels(revision, version, artifact='core'):
    return {'org.opencontainers.image.revision': revision, 'org.opencontainers.image.version': version,
            'org.subcult.archive-artifact': artifact}


class CoreRevisionTests(unittest.TestCase):
    def test_waits_until_a_candidate_is_published(self):
        self.assertIsNone(core_revision([A, B], lambda revision: None, '0.4.9'))

    def test_accepts_a_later_main_commit_that_carries_the_version(self):
        # Gitea may build the newest commit of a batch copied from GitHub.
        published = {B: ('sha256:' + 'd' * 64, labels(B, '0.4.9'))}
        self.assertEqual(core_revision([A, B, C], published.get, '0.4.9'), (B, 'sha256:' + 'd' * 64))

    def test_skips_images_of_another_version(self):
        published = {A: ('sha256:' + 'e' * 64, labels(A, '0.4.8'))}
        self.assertIsNone(core_revision([A], published.get, '0.4.9'))

    def test_rejects_an_image_whose_labels_do_not_match_its_tag(self):
        for wrong in (labels(B, '0.4.9'), labels(A, '0.4.9', 'reference')):
            with self.assertRaises(ValueError):
                core_revision([A], {A: ('sha256:' + 'f' * 64, wrong)}.get, '0.4.9')


if __name__ == '__main__':
    unittest.main()
